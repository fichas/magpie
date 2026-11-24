package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/agyjournal"
	"github.com/yetone/magpie/internal/provider"
)

// generateCount counts the Code Assist generations a fake was asked for,
// and answers each as say does.
func generateCount(n *atomic.Int32, say func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "generateContent") || strings.Contains(r.URL.Path, "streamGenerateContent") {
			n.Add(1)
		}
		say(w, r)
	}
}

// unavailable is Code Assist's 503, which magpie asks another member after.
func unavailable(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	io.WriteString(w, `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE"}}`)
}

// emptyCodeAssist is a stream that ends with nothing said, which magpie asks
// again for (emptyRetries) and then asks another member after.
func emptyCodeAssist(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, sse(`data: {"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":0}}}`))
}

func groupTurn(t *testing.T, s *Server, stream bool, key string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"model":"group/g","max_tokens":100,"stream":` + map[bool]string{true: "true", false: "false"}[stream] + `,"system":"Be brief.","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000" // an agent on this computer
	if key != "" {
		r.Header.Set(agyjournal.Header, key)
	}
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// By default an Antigravity account's 503, or its empty reply, is passed
// over for the group's next member (the fallback the AGY tests below hold
// off).
func TestAgyDefaultFallsBack(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	for name, say := range map[string]func(http.ResponseWriter, *http.Request){"503": unavailable, "empty": emptyCodeAssist} {
		var n atomic.Int32
		s, _ := antigravityGroupFor(t, []string{"a@example.com", "b@example.com"}, "gemini-3-flash", generateCount(&n, say))
		if rec := groupTurn(t, s, true, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "from the other") {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if n.Load() < 2 {
			t.Fatalf("%s: Google asked %d times", name, n.Load())
		}
	}
}

// With prompt=agy the request is sent once: Google's 503 is the client's
// answer, in Google's words, and the next member is never asked.
func TestAgySingleSendKeepsTheFailure(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	for _, stream := range []bool{false, true} {
		var n atomic.Int32
		s, _ := antigravityGroupFor(t, []string{"a@example.com", "b@example.com"}, "gemini-3-flash", generateCount(&n, unavailable))
		setAgyCompat(t, true)
		rec := groupTurn(t, s, stream, "")
		if strings.Contains(rec.Body.String(), "from the other") {
			t.Fatalf("stream %v: the next member was asked after an AGY send: %s", stream, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "currently unavailable") {
			t.Fatalf("stream %v: Google's own failure wasn't the answer: %d %s", stream, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), agyjournal.ErrNoResend.Error()) {
			t.Fatalf("stream %v: the guard's refusal was told in place of Google's: %s", stream, rec.Body)
		}
		if got := n.Load(); got != 1 {
			t.Fatalf("stream %v: Google asked %d times, want 1", stream, got)
		}
	}
}

// An AGY send's empty reply is the answer: not asked again, nor of the
// next account or member.
func TestAgySingleSendNoEmptyRetry(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	var n atomic.Int32
	s, _ := antigravityGroupFor(t, []string{"a@example.com", "b@example.com"}, "gemini-3-flash", generateCount(&n, emptyCodeAssist))
	setAgyCompat(t, true)
	rec := groupTurn(t, s, true, "")
	if got := n.Load(); got != 1 || strings.Contains(rec.Body.String(), "from the other") {
		t.Fatalf("an AGY empty reply: Google asked %d times, the client got %s", got, rec.Body)
	}
}

// A send that never reached Google (the connection failed before any
// byte went) fences nothing: the next member is asked as before.
func TestAgyUnsentFallsBack(t *testing.T) {
	t.Cleanup(provider.AntigravityBuiltinForTest())
	s, _ := antigravityGroupFor(t, []string{"a@example.com"}, "gemini-3-flash", unavailable)
	setAgyCompat(t, true)
	s.client = &http.Client{Transport: countTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "googleapis.com") {
			return nil, errors.New("dial tcp 142.250.0.1:443: connect: connection refused")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	if rec := groupTurn(t, s, false, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "from the other") {
		t.Fatalf("an unsent AGY try didn't fall back: %d %s", rec.Code, rec.Body)
	}
}

// keyedSetup is a group on Antigravity with the AGY profile, served
// natively here (antigravityBuiltin), its journal in the test's folder.
func keyedSetup(t *testing.T, say func(w http.ResponseWriter, r *http.Request)) (*Server, *atomic.Int32) {
	t.Helper()
	t.Cleanup(provider.AntigravityBuiltinForTest())
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "1")
	var n atomic.Int32
	s, _ := antigravityGroupFor(t, []string{"a@example.com"}, "gemini-3-flash", generateCount(&n, say))
	setAgyCompat(t, true)
	path := filepath.Join(t.TempDir(), "agy-journal.db")
	old := agyJournalPath
	agyJournalPath = func() string { return path }
	t.Cleanup(func() {
		agyJournalPath = old
		agyjournal.CloseShared()
	})
	return s, &n
}

// A keyed request is answered once; its repeat gets the same bytes back,
// marked, with Google not asked again, buffered or streamed. Another body
// under the key is a conflict, a malformed key a 400.
func TestAgyIdempotencyKeyReplays(t *testing.T) {
	s, n := keyedSetup(t, fakeCodeAssist(&agyEnvelope{}))
	for _, stream := range []bool{false, true} {
		key := "turn-" + map[bool]string{true: "stream", false: "whole"}[stream]
		n.Store(0)
		first := groupTurn(t, s, stream, key)
		if first.Code != 200 || !strings.Contains(first.Body.String(), "from google") || first.Header().Get(agyjournal.ReplayedHeader) != "" {
			t.Fatalf("stream %v, first: %d %v %s", stream, first.Code, first.Header(), first.Body)
		}
		again := groupTurn(t, s, stream, key)
		if again.Code != 200 || again.Body.String() != first.Body.String() || again.Header().Get(agyjournal.ReplayedHeader) != "true" {
			t.Fatalf("stream %v, repeat: %d %v\n%q\nwant %q", stream, again.Code, again.Header(), again.Body, first.Body)
		}
		if got := n.Load(); got != 1 {
			t.Fatalf("stream %v: Google asked %d times for a key, want 1", stream, got)
		}
	}
	body := `{"model":"group/g","max_tokens":100,"stream":false,"system":"Something else.","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set(agyjournal.Header, "turn-whole")
	s.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("another body under the key: %d %s", rec.Code, rec.Body)
	}
	if rec := groupTurn(t, s, false, "has space"); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed key: %d %s", rec.Code, rec.Body)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("a refused key reached Google: %d", got)
	}
}

// A keyed request that may have reached Google and failed is uncertain:
// its repeat is a 409, never sent again.
func TestAgyIdempotencyUncertainIsNotResent(t *testing.T) {
	s, n := keyedSetup(t, unavailable)
	if rec := groupTurn(t, s, false, "k"); rec.Code < 400 {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	if rec := groupTurn(t, s, false, "k"); rec.Code != http.StatusConflict {
		t.Fatalf("repeat of an uncertain request: %d %s", rec.Code, rec.Body)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("Google asked %d times, want 1", got)
	}
}

// With the native Antigravity off (as magpie ships), a key changes
// nothing: no journal is opened, and a repeat is served again.
func TestAgyKeyIgnoredWithoutTheBuiltin(t *testing.T) {
	s, n := keyedSetup(t, fakeCodeAssist(&agyEnvelope{}))
	t.Setenv("MAGPIE_ANTIGRAVITY_BUILTIN", "0")
	groupTurn(t, s, false, "k")
	if rec := groupTurn(t, s, false, "k"); rec.Code != 200 || rec.Header().Get(agyjournal.ReplayedHeader) != "" {
		t.Fatalf("repeat: %d %v", rec.Code, rec.Header())
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("Google asked %d times, want each request", got)
	}
}
