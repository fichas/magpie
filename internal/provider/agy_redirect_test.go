package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/agyjournal"
)

// A 307 sends a POST's body again to where it points. An AGY single send
// is never sent again: the redirect is its answer.
func TestKeyedFollowsNoRedirectForAnAGYSend(t *testing.T) {
	var moved atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			moved.Add(1)
			return
		}
		http.Redirect(w, r, "/moved", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	post := func(ctx context.Context) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1internal:generateContent", strings.NewReader(`{}`))
		res, err := Keyed(srv.Client()).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post(context.Background()); code != 200 || moved.Load() != 1 {
		t.Fatalf("by default: %d, moved asked %d times", code, moved.Load())
	}
	ctx, _ := agyjournal.Bind(context.Background())
	if code := post(agyjournal.Attempt(ctx)); code != http.StatusTemporaryRedirect || moved.Load() != 1 {
		t.Fatalf("an AGY send: %d, moved asked %d times", code, moved.Load())
	}
}
