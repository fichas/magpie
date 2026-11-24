package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/agyjournal"
)

// markSeen records whether a request reached the transport below with
// Go's replay mark on it.
type markSeen struct {
	next   http.RoundTripper
	marked atomic.Bool
}

func (m *markSeen) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, ok := r.Header["X-Idempotency-Key"]; ok {
		m.marked.Store(true)
	}
	return m.next.RoundTrip(r)
}

// A new connection that ended before the request's write began is tried
// again by replayable (TestEndedBeforeWriteBeganIsSentAgain), and every
// request is marked for Go's own replay. An AGY single send is neither:
// sent once, its failure returned.
func TestAgySingleSendNotReplayedByTransport(t *testing.T) {
	closedUnsentClient(t) // the short pauses
	post := func(ctx context.Context, g *goEnded) (*markSeen, error) {
		m := &markSeen{next: g}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://127.0.0.1:1/v1internal:streamGenerateContent", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		res, err := (&http.Client{Transport: replayable{m}}).Do(req)
		if err == nil {
			res.Body.Close()
		}
		return m, err
	}
	g := &goEnded{ends: 1, err: io.EOF}
	if m, err := post(context.Background(), g); err != nil || g.tries.Load() != 2 || !m.marked.Load() {
		t.Fatalf("by default: %v, %d tries, marked %v", err, g.tries.Load(), m.marked.Load())
	}
	// goEnded calls the trace it is handed: replayable adds its own only
	// on the path it retries on
	ctx, _ := agyjournal.Bind(httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{}))
	g = &goEnded{ends: 1, err: io.EOF}
	m, err := post(agyjournal.Attempt(ctx), g)
	if err == nil || g.tries.Load() != 1 {
		t.Fatalf("an AGY send: %v, %d tries, want its failure after 1", err, g.tries.Load())
	}
	if m.marked.Load() {
		t.Fatal("an AGY send was marked for Go's replay")
	}
}
