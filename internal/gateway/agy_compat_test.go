package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/agycompat"
	"github.com/yetone/magpie/internal/provider"
)

// agyEnvelope is what a fake Code Assist was last sent: the system
// instruction, and each declared function's name and description.
type agyEnvelope struct {
	mu     sync.Mutex
	system string
	tools  map[string]string
}

func (e *agyEnvelope) read() (string, map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.system, e.tools
}

// fakeCodeAssist answers every request and keeps what it was sent.
func fakeCodeAssist(e *agyEnvelope) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			Request struct {
				SystemInstruction struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"systemInstruction"`
				Tools []struct {
					FunctionDeclarations []struct {
						Name        string `json:"name"`
						Description string `json:"description"`
					} `json:"functionDeclarations"`
				} `json:"tools"`
			} `json:"request"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &env)
		e.mu.Lock()
		e.system, e.tools = "", map[string]string{}
		for _, p := range env.Request.SystemInstruction.Parts {
			e.system += p.Text
		}
		for _, t := range env.Request.Tools {
			for _, d := range t.FunctionDeclarations {
				e.tools[d.Name] = d.Description
			}
		}
		e.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(`data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"from google"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}}}`))
	}
}

// claudeCodeAGYSystem is the head of Claude Code 2.1.325's system prompt as
// it reaches the gateway, a settings path in it.
const claudeCodeAGYSystem = "x-anthropic-billing-header: cc_version=2.1.325.a1b; cc_entrypoint=cli; cch=00000;\n" +
	"You are Claude Code, Anthropic's official CLI for Claude.\n" +
	"You are an interactive agent that helps users with software engineering tasks.\n\n" +
	"- Claude Code is available as a CLI in the terminal, desktop app (Mac/Windows), web app (claude.ai/code), and IDE extensions (VS Code, JetBrains).\n" +
	"- Keep the user's settings in ~/.claude/settings.json as they are.\n"

func agyMessagesTurn(t *testing.T, s *Server, path, ua string) (int, string) {
	t.Helper()
	var body string
	switch path {
	case "/v1/messages":
		body = `{"model":"antigravity/gemini-3-flash","max_tokens":100,"system":` + quote(claudeCodeAGYSystem) + `,` +
			`"tools":[{"name":"Read","description":"Read a file. Claude can read CLAUDE.md and ~/.claude/agents/x.md.","input_schema":{"type":"object","properties":{"file_path":{"type":"string","description":"Claude's path"}}}}],` +
			`"messages":[{"role":"user","content":"Claude, hi"}]}`
	case "/v1/chat/completions":
		body = `{"model":"antigravity/gemini-3-flash","stream":false,"messages":[{"role":"system","content":` + quote(claudeCodeAGYSystem) + `},{"role":"user","content":"hi"}]}`
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if ua != "" {
		r.Header.Set("User-Agent", ua)
	}
	s.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func setAgyCompat(t *testing.T, on bool) {
	t.Helper()
	p, err := provider.Find("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	p.AgyCompat = on
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	if p, _ := provider.Find("antigravity"); p.AgyCompat != on {
		t.Fatalf("prompt=agy isn't kept as %v", on)
	}
}

// With prompt=agy a Claude Code turn on the built-in Antigravity goes with
// Antigravity's identity, no billing line and no product bullet; the
// settings path, CLAUDE.md, the tool's name and its schema as written; the
// tool's description in Antigravity's words. The default sends Claude
// Code's identity line, which Google turns away since 11-15.
func TestAgyCompatSendsClaudeCodeAsAntigravity(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest()) // the retired built-in route
	e := &agyEnvelope{}
	s, _ := antigravityGroupFor(t, []string{"u@example.com"}, "gemini-3-flash", fakeCodeAssist(e))

	if code, out := agyMessagesTurn(t, s, "/v1/messages", "claude-cli/2.1.325 (external, cli)"); code != 200 || !strings.Contains(out, "from google") {
		t.Fatalf("default: %d %s", code, out)
	}
	if sys, _ := e.read(); !strings.Contains(sys, "You are Claude Code, Anthropic's official CLI for Claude.") || strings.Contains(sys, agycompat.Identity) {
		t.Fatalf("by default the prompt went as %q", sys)
	}

	setAgyCompat(t, true)
	if code, out := agyMessagesTurn(t, s, "/v1/messages", "claude-cli/2.1.325 (external, cli)"); code != 200 || !strings.Contains(out, "from google") {
		t.Fatalf("prompt=agy: %d %s", code, out)
	}
	sys, tools := e.read()
	if !strings.Contains(sys, agycompat.Identity) {
		t.Fatalf("no Antigravity identity in %q", sys)
	}
	for _, gone := range []string{"You are Claude Code", "billing-header", "cc_version", "desktop app (Mac/Windows)"} {
		if strings.Contains(sys, gone) {
			t.Fatalf("%q went: %q", gone, sys)
		}
	}
	if !strings.Contains(sys, "~/.claude/settings.json") {
		t.Fatalf("the settings path changed: %q", sys)
	}
	desc, ok := tools["Read"]
	if !ok {
		t.Fatalf("the tool's name changed: %v", tools)
	}
	if desc != "Read a file. Antigravity can read CLAUDE.md and ~/.claude/agents/x.md." {
		t.Fatalf("the tool's description went as %q", desc)
	}
}

// The profile is an Anthropic Messages client's: a Chat client's prompt on
// the same subscription goes as by default, and so does one sent as
// written.
func TestAgyCompatOnlyForMessages(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	e := &agyEnvelope{}
	s, _ := antigravityGroupFor(t, []string{"u@example.com"}, "gemini-3-flash", fakeCodeAssist(e))
	setAgyCompat(t, true)
	if code, out := agyMessagesTurn(t, s, "/v1/chat/completions", ""); code != 200 {
		t.Fatalf("chat: %d %s", code, out)
	}
	if sys, _ := e.read(); strings.Contains(sys, agycompat.Identity) || !strings.Contains(sys, "You are Claude Code") {
		t.Fatalf("a Chat client's prompt went as %q", sys)
	}

	p, _ := provider.Find("antigravity")
	p.PromptAsWritten = true
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	agyMessagesTurn(t, s, "/v1/messages", "")
	if sys, _ := e.read(); strings.Contains(sys, agycompat.Identity) || !strings.Contains(sys, "x-anthropic-billing-header") {
		t.Fatalf("as written, the prompt went as %q", sys)
	}
}

// DeepSeek Harness's User-Agent says its generic model-identity line is its
// own; another client's line goes as written.
func TestAgyCompatDSHUserAgent(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	e := &agyEnvelope{}
	s, _ := antigravityGroupFor(t, []string{"u@example.com"}, "gemini-3-flash", fakeCodeAssist(e))
	setAgyCompat(t, true)
	const line = "You are a coding agent powered by the gemini-3-flash model."
	turn := func(ua string) string {
		body := `{"model":"antigravity/gemini-3-flash","max_tokens":100,"system":` + quote(line+"\nKeep going.") + `,"messages":[{"role":"user","content":"hi"}]}`
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		r.Header.Set("User-Agent", ua)
		s.Handler().ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", ua, rec.Code, rec.Body.String())
		}
		sys, _ := e.read()
		return sys
	}
	if sys := turn("deepseek-harness/1.4.0 (desktop)"); !strings.HasPrefix(sys, agycompat.Identity) {
		t.Fatalf("DSH's line went as %q", sys)
	}
	if sys := turn("some-agent/1.0"); !strings.HasPrefix(sys, line) {
		t.Fatalf("another client's line went as %q", sys)
	}
}
