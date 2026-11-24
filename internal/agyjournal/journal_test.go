package agyjournal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openStore(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "agy-journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A claim is won once; a swap needs the generation it read; an expired
// record is claimed again; a delete needs its generation too.
func TestSQLiteCompareAndSwap(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if ok, err := s.CompareAndSwap(ctx, "k", 0, []byte("a"), time.Hour); !ok || err != nil {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, _ := s.CompareAndSwap(ctx, "k", 0, []byte("b"), time.Hour); ok {
		t.Fatal("a second claim won")
	}
	if ok, _ := s.CompareAndSwap(ctx, "k", 2, []byte("b"), time.Hour); ok {
		t.Fatal("a swap at the wrong generation won")
	}
	if ok, _ := s.CompareAndSwap(ctx, "k", 1, []byte("b"), time.Hour); !ok {
		t.Fatal("the swap at its generation lost")
	}
	if raw, gen, _ := s.Load(ctx, "k"); string(raw) != "b" || gen != 2 {
		t.Fatalf("read %q at %d", raw, gen)
	}
	if ok, _ := s.DeleteIfGeneration(ctx, "k", 1); ok {
		t.Fatal("deleted at the wrong generation")
	}
	later := time.Now().Add(2 * time.Hour)
	s.now = func() time.Time { return later }
	if raw, gen, _ := s.Load(ctx, "k"); raw != nil || gen != 0 {
		t.Fatalf("expired record read as %q at %d", raw, gen)
	}
	if ok, _ := s.CompareAndSwap(ctx, "k", 0, []byte("c"), time.Hour); !ok {
		t.Fatal("an expired record couldn't be claimed again")
	}
}

// Of many concurrent claims, through two handles on one file as two
// magpie processes would have, exactly one wins.
func TestSQLiteOneClaimWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agy-journal.db")
	a, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := range 16 {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := s.CompareAndSwap(context.Background(), "k", 0, []byte("x"), time.Hour); ok && err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d claims won", won)
	}
}

func TestKey(t *testing.T) {
	h := http.Header{}
	if _, present, err := Key(h); present || err != nil {
		t.Fatal("no header read as one")
	}
	for _, bad := range [][]string{{""}, {"a", "b"}, {"has space"}, {strings.Repeat("x", 257)}, {"ключ"}} {
		h := http.Header{Header: bad}
		if _, present, err := Key(h); !present || err == nil || err.Status != 400 {
			t.Errorf("%q: present %v, %v", bad, present, err)
		}
	}
	h = http.Header{"idempotency-key": {"abc-123"}}
	if k, present, err := Key(h); !present || err != nil || k != "abc-123" {
		t.Fatalf("lower-case header: %q %v %v", k, present, err)
	}
}

func req(body string, stream bool) Request {
	return Request{Caller: "local:tok", Protocol: "anthropic", Model: "antigravity/gemini-3-flash", Stream: stream, Body: []byte(body)}
}

const streamReply = "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"{\\\"type\\\":\\\"message_stop\\\"}\"}}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\"}\n\n"

const streamStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// A stream is kept at its message_stop, before that frame is written on,
// and a repeat gets it back byte for byte, marked; a message_stop quoted in
// the text commits nothing.
func TestStreamCommitsAtMessageStopAndReplays(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	replay, res, rerr := Reserve(ctx, s, "k1", req("b", true))
	if replay != nil || rerr != nil || res == nil {
		t.Fatalf("first: %v %v", replay, rerr)
	}
	rec := httptest.NewRecorder()
	w := res.Writer(rec, nil)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set(ReplayedHeader, "true") // an upstream's, never passed on
	w.Write([]byte(streamReply))
	if res.Committed() {
		t.Fatal("committed before message_stop, at a quoted one")
	}
	if _, _, rerr := Reserve(ctx, s, "k1", req("b", true)); rerr == nil || rerr.Status != 409 {
		t.Fatalf("while running: %v", rerr)
	}
	w.Write([]byte(streamStop))
	if !res.Committed() {
		t.Fatal("not committed at message_stop")
	}
	res.Finish(true, true)
	if rec.Header().Get(ReplayedHeader) != "" {
		t.Fatal("the upstream's replayed header went out")
	}

	replay, res2, rerr := Reserve(ctx, s, "k1", req("b", true))
	if replay == nil || res2 != nil || rerr != nil {
		t.Fatalf("repeat: %v %v", res2, rerr)
	}
	out := httptest.NewRecorder()
	replay.Write(out)
	if out.Body.String() != streamReply+streamStop || out.Header().Get(ReplayedHeader) != "true" || out.Header().Get("Content-Type") != "text/event-stream" || out.Code != 200 {
		t.Fatalf("replayed %d %v %q", out.Code, out.Header(), out.Body.String())
	}
	if _, _, rerr := Reserve(ctx, s, "k1", req("other", true)); rerr == nil || rerr.Status != 409 {
		t.Fatalf("another body under the key: %v", rerr)
	}
	if _, _, rerr := Reserve(ctx, s, "k1", req("b", false)); rerr == nil || rerr.Status != 409 {
		t.Fatalf("the same body unstreamed: %v", rerr)
	}
	other := req("b", true)
	other.Caller = "local:another"
	if replay, res, _ := Reserve(ctx, s, "k1", other); replay != nil || res == nil {
		t.Fatal("another caller's key read this caller's reply")
	}
}

// A stream cut before message_stop, after a send, stays uncertain: 409
// until it expires. One that sent nothing is released for a retry.
func TestUncertainStaysAndUnsentReleases(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_, res, _ := Reserve(ctx, s, "cut", req("b", true))
	w := res.Writer(httptest.NewRecorder(), nil)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte(streamReply))
	res.Finish(true, true)
	if _, _, rerr := Reserve(ctx, s, "cut", req("b", true)); rerr == nil || rerr.Status != 409 {
		t.Fatalf("after a cut stream: %v", rerr)
	}

	_, res, _ = Reserve(ctx, s, "unsent", req("b", false))
	w = res.Writer(httptest.NewRecorder(), nil)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(502)
	w.Write([]byte(`{"type":"error"}`))
	res.Finish(false, true)
	if replay, res, rerr := Reserve(ctx, s, "unsent", req("b", false)); replay != nil || res == nil || rerr != nil {
		t.Fatalf("after nothing was sent: %v %v", replay, rerr)
	}
}

// A buffered reply is kept when it is a whole message, and an error
// reply never is, sent or not.
func TestBufferedCommitAndErrorsNot(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_, res, _ := Reserve(ctx, s, "ok", req("b", false))
	w := res.Writer(httptest.NewRecorder(), nil)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"type":"message","content":[]}`))
	res.Finish(true, true)
	if replay, _, _ := Reserve(ctx, s, "ok", req("b", false)); replay == nil || string(replay.Body) != `{"type":"message","content":[]}` {
		t.Fatal("the whole message wasn't kept")
	}

	_, res, _ = Reserve(ctx, s, "err", req("b", false))
	w = res.Writer(httptest.NewRecorder(), nil)
	w.WriteHeader(429)
	w.Write([]byte(`{"type":"error"}`))
	res.Finish(true, true)
	if _, _, rerr := Reserve(ctx, s, "err", req("b", false)); rerr == nil || rerr.Status != 409 {
		t.Fatalf("an error reply after a send: %v", rerr)
	}
}

// A reply past MaxResponseBytes is never kept.
func TestOversizeNotKept(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_, res, _ := Reserve(ctx, s, "big", req("b", true))
	w := res.Writer(httptest.NewRecorder(), nil)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte("data: {\"type\":\"x\",\"pad\":\"" + strings.Repeat("a", MaxResponseBytes) + "\"}\n\n"))
	w.Write([]byte(streamStop))
	res.Finish(true, true)
	if res.Committed() {
		t.Fatal("an oversize stream was kept")
	}
}

func TestReserveRefusals(t *testing.T) {
	s := openStore(t)
	r := req("b", false)
	r.Federated = true
	if _, _, rerr := Reserve(context.Background(), s, "k", r); rerr == nil || rerr.Status != 400 {
		t.Fatalf("federated: %v", rerr)
	}
	r = req("b", false)
	r.Caller = ""
	if _, _, rerr := Reserve(context.Background(), s, "k", r); rerr == nil || rerr.Status != 503 {
		t.Fatalf("no caller: %v", rerr)
	}
	if _, _, rerr := Reserve(context.Background(), nil, "k", req("b", false)); rerr == nil || rerr.Status != 503 {
		t.Fatalf("no store: %v", rerr)
	}
}

// Once an AGY attempt's headers are written nothing more is begun; a
// non-AGY send, or an AGY one that never wrote its headers, fences nothing.
func TestGuard(t *testing.T) {
	ctx, g := Bind(context.Background())
	c, err := Begin(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	httptrace.ContextClientTrace(c).WroteHeaders()
	if !g.Sent() || g.Fenced() {
		t.Fatal("a non-AGY send fenced")
	}
	if _, err := Begin(ctx, true); err != nil {
		t.Fatal("an AGY attempt with nothing sent was refused")
	}
	if g.Fenced() {
		t.Fatal("an AGY attempt fenced before its headers went")
	}
	c, _ = Begin(ctx, true)
	httptrace.ContextClientTrace(c).WroteHeaders()
	if !g.Fenced() {
		t.Fatal("an AGY send didn't fence")
	}
	if _, err := Begin(ctx, false); err != ErrNoResend {
		t.Fatalf("after an AGY send: %v", err)
	}
	// a round after a completed one is a generation of its own: one more
	// send, and fenced again once it goes
	Advance(ctx)
	c, err = Begin(ctx, true)
	if err != nil {
		t.Fatalf("after Advance: %v", err)
	}
	httptrace.ContextClientTrace(c).WroteHeaders()
	if _, err := Begin(ctx, true); err != ErrNoResend {
		t.Fatalf("the round's send didn't fence again: %v", err)
	}
	if c, err := Begin(context.Background(), true); err != nil || c == nil {
		t.Fatal("no guard changed something")
	}
}

// The shared journal opens once per path, prunes what expired, and lets
// go of its file when closed.
func TestShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agy-journal.db")
	a, err := Shared(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseShared)
	if b, _ := Shared(path); b != a {
		t.Fatal("opened twice for one path")
	}
	CloseShared()
	b, err := Shared(path)
	if err != nil || b == a {
		t.Fatalf("not opened again after close: %v", err)
	}
}
