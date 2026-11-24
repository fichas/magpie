package agycompat

import (
	"strings"
	"testing"
)

// Claude Code's, the Agent SDK's and DeepSeek Harness's identity lines go
// as Antigravity's, the rest of the prompt and CLAUDE.md as written, and a
// second pass changes nothing (a late directive may go through twice).
func TestSystemCompatibility(t *testing.T) {
	for name, input := range map[string]string{
		"cc":  "You are Claude Code, Anthropic's official CLI for Claude.",
		"sdk": "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
		"dsh": "You are an AI agent powered by DeepSeek Harness.",
	} {
		t.Run(name, func(t *testing.T) {
			got := System(input+"\nKeep approval requirements. Read CLAUDE.md, `CLAUDE.md` and CLAUDE.md.", false)
			if !strings.Contains(got, Identity) {
				t.Fatalf("no identity in %q", got)
			}
			if !strings.Contains(got, "Keep approval requirements. Read CLAUDE.md, `CLAUDE.md` and CLAUDE.md.") {
				t.Fatalf("the rest changed: %q", got)
			}
			if again := System(got, false); again != got {
				t.Fatalf("a second pass changed it: %q", again)
			}
		})
	}
	for _, marker := range []string{"x-anthropic-billing-header:", "X_ANTHROPIC_BILLING_HEADER:", "x-anti gravity-billing-header:"} {
		if got := System(marker+" cc_version=2.1.325; cch=00000;\nKeep this.", false); got != "\nKeep this." {
			t.Fatalf("%s: %q", marker, got)
		}
	}
	if got := System("Use Claude and Anthropic.", false); got != "Use Antigravity and Antigravity." {
		t.Fatalf("product names: %q", got)
	}
}

// A path in the system prompt names a file on disk: it stays as written,
// where AsterGate's copy said ~/.Antigravity/settings.json.
func TestSystemKeepsPaths(t *testing.T) {
	in := "Settings live in ~/.claude/settings.json and .claude/agents; see claude.json, C:\\Users\\me\\.claude and https://docs.anthropic.com/en. Ask Claude."
	want := "Settings live in ~/.claude/settings.json and .claude/agents; see claude.json, C:\\Users\\me\\.claude and https://docs.anthropic.com/en. Ask Antigravity."
	if got := System(in, false); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestDSHAndProductParagraphBoundaries(t *testing.T) {
	model := "You are a coding agent powered by the example-model model."
	if got := System(model, false); got != model {
		t.Fatalf("not DSH: %q", got)
	}
	if got := System(model, true); got != Identity {
		t.Fatalf("DSH: %q", got)
	}
	in := "The DeepSeek Harness implementation checkout is at /synthetic.\n\nKeep approval requirements.\n\nYou are interacting with the user through the DeepSeek Harness Web GUI at https://example.test.\n\nKeep this too."
	got := System(in, true)
	for _, gone := range []string{"/synthetic", "example.test"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%s kept: %q", gone, got)
		}
	}
	for _, kept := range []string{"Keep approval requirements.", "Keep this too."} {
		if !strings.Contains(got, kept) {
			t.Fatalf("%s lost: %q", kept, got)
		}
	}
	got = System("- Claude Code is available as a CLI, desktop app, and IDE extension.\n- Fast mode uses Claude; toggle /fast.\n- Model IDs include Claude Sonnet and Opus.\n- Preserve workspace restrictions.\n", false)
	if got != "- Preserve workspace restrictions.\n" {
		t.Fatalf("bullets: %q", got)
	}
	if !DSHUserAgent("deepseek-harness/1.2.3 (desktop)") || DSHUserAgent("not-deepseek-harness/1") || DSHUserAgent("deepseek-harness/") {
		t.Fatal("DSHUserAgent")
	}
	untouched := "Keep this.\n\n\n  \nKeep that.\n- Fast mode for Antigravity uses /fastest in this project.\n"
	if got := System(untouched, false); got != untouched {
		t.Fatalf("unrelated text changed: %q", got)
	}
}

func TestToolDescriptionKeepsExecutableTokens(t *testing.T) {
	text := "Ask Claude or Anthropic; read CLAUDE.md, .claude and /tmp/Claude/file. Visit https://Anthropic.example/test.\nKeep\tspaces."
	want := "Ask Antigravity or Antigravity; read CLAUDE.md, .claude and /tmp/Claude/file. Visit https://Anthropic.example/test.\nKeep\tspaces."
	if got := ToolDescription(text); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
