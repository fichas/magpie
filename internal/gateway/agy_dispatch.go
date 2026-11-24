package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/yetone/magpie/internal/agyjournal"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// The AGY profile's dispatch guarantees (internal/agyjournal), for an
// Anthropic Messages request: every one is served under a Guard, so that
// once a send for an Antigravity account with the profile on may have
// reached Google, nothing sends the request again (agyBegin in forward);
// and one carrying an Idempotency-Key, while the built-in Antigravity is
// served here (settings.AntigravityBuiltin), is reserved before anything
// is asked, and its complete reply kept and given back on a repeat.

// agyJournalPath is where the journal is kept; a var so tests can move it.
var agyJournalPath = func() string { return appdir.Join(appdir.Config(), "agy-journal.db") }

// serveMessages serves an Anthropic Messages request under the AGY
// guarantees, then as every other request (serveAgent). The reply is
// captured outside every writer serve adds, so what is kept is what the
// client was sent.
func (s *Server) serveMessages(w http.ResponseWriter, r *http.Request, body []byte) {
	ctx, g := agyjournal.Bind(r.Context())
	r = r.WithContext(ctx)
	key, present, kerr := agyjournal.Key(r.Header)
	if !present || !settings.AntigravityBuiltin() {
		// a key means nothing while no native Antigravity is served here
		s.serveAgent(w, r, provider.Anthropic, body)
		return
	}
	if kerr != nil {
		writeError(w, provider.Anthropic, kerr.Status, kerr.Message)
		return
	}
	var store agyjournal.Store
	if j, err := agyjournal.Shared(agyJournalPath()); err == nil {
		store = j
	}
	c := callerOf(r)
	replay, res, rerr := agyjournal.Reserve(ctx, store, key, agyjournal.Request{
		Caller:    agyCaller(r),
		Federated: magpieAsks(r) || c.via != "",
		Protocol:  string(provider.Anthropic),
		Model:     modelOf(body),
		Session:   c.session,
		UserAgent: c.ua,
		Stream:    streamOf(body),
		Body:      body,
	})
	if rerr != nil {
		writeError(w, provider.Anthropic, rerr.Status, rerr.Message)
		return
	}
	if replay != nil {
		// the reply as it went the first time, with no new usage: no
		// provider is asked, so nothing is counted
		replay.Write(w)
		return
	}
	cw := res.Writer(w, g.AGY)
	defer func() { res.Finish(g.Sent(), g.AGY()) }()
	s.serveAgent(cw, r, provider.Anthropic, body)
}

// agyCaller is who a key is scoped to: on this computer, the token the
// client sent (any, as loopback takes any); from another, the gateway key
// it came in with. "" for none, which a key is refused for.
func agyCaller(r *http.Request) string {
	k := callerKey(r)
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	switch {
	case local(r):
		return "local:" + sum(k)
	case k != "" && sharedWith(r):
		return "key:" + sum(k)
	}
	return ""
}
