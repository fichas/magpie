// Package agyjournal is the AGY profile's dispatch guarantees on a native
// Antigravity account, as AsterGate's agyjournal has them (nwverse/astergate
// #3466), for magpie's gateway:
//
//   - single send: once a request for an account with the profile on may
//     have reached Google (its headers were written upstream), nothing
//     sends that client request upstream again: no re-ask, no other
//     account, no fallback. Its failure goes to the client (Guard);
//   - Idempotency-Key replay: a key is reserved before generation, scoped
//     to the caller, and bound to the exact request; the completed response
//     is kept (up to MaxResponseBytes, for Retention) and a repeat of the
//     same request gets it back byte for byte, with no new usage
//     (Journal, Capture). A reservation whose outcome is uncertain is
//     never released or taken over: it answers 409 until it expires.
package agyjournal

import (
	"context"
	"errors"
	"net/http/httptrace"
	"sync"
)

// ErrNoResend is what an upstream send is refused with once a send for an
// account with the AGY profile on may have reached Google for the same
// client request.
var ErrNoResend = errors.New("AGY single send: the request may already have reached Antigravity, so it is not sent again")

// Guard follows one client request's upstream sends.
type Guard struct {
	mu     sync.Mutex
	sent   bool // some attempt's headers were written upstream
	fenced bool // an AGY attempt's were: nothing more goes
	agy    bool // an AGY attempt was begun, sent or not
}

type guardKey struct{}

// Bind is ctx with a new Guard for one client request.
func Bind(ctx context.Context) (context.Context, *Guard) {
	g := &Guard{}
	return context.WithValue(ctx, guardKey{}, g), g
}

// From is ctx's Guard, nil for none.
func From(ctx context.Context) *Guard {
	g, _ := ctx.Value(guardKey{}).(*Guard)
	return g
}

// Begin is called right before an upstream send under ctx; agy says the
// send is for an account with the AGY profile on. It refuses with
// ErrNoResend once an AGY send went out for the request, and otherwise
// gives the context to send under, which marks the send as gone once its
// headers are written: a dial, DNS or TLS failure before then sent
// nothing, and leaves fallback and release as they were. With no Guard in
// ctx it changes nothing.
func Begin(ctx context.Context, agy bool) (context.Context, error) {
	g := From(ctx)
	if g == nil {
		return ctx, nil
	}
	g.mu.Lock()
	fenced := g.fenced
	if agy {
		g.agy = true
	}
	g.mu.Unlock()
	if fenced {
		return ctx, ErrNoResend
	}
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() {
			g.mu.Lock()
			g.sent = true
			if agy {
				g.fenced = true
			}
			g.mu.Unlock()
		},
	}), nil
}

type attemptKey struct{}

// Attempt is ctx marked as an attempt on an account with the AGY profile
// on, for the send below it (Begin's agy, Single).
func Attempt(ctx context.Context) context.Context {
	return context.WithValue(ctx, attemptKey{}, true)
}

// IsAttempt says whether ctx is an AGY attempt's (Attempt).
func IsAttempt(ctx context.Context) bool {
	ok, _ := ctx.Value(attemptKey{}).(bool)
	return ok
}

// Single says whether a send under ctx goes out once only, below magpie's
// own retries too: an AGY attempt's, under a Guard. A transport sends it
// once, on whatever connection it gets, and never again on another (no
// replay of an "unsent" request, no idempotent-request retry, no second
// upload after a lost ping).
func Single(ctx context.Context) bool { return From(ctx) != nil && IsAttempt(ctx) }

// Fenced is From(ctx).Fenced.
func Fenced(ctx context.Context) bool { return From(ctx).Fenced() }

// Fenced says whether an AGY send went out: the request is not sent
// upstream again, by any route.
func (g *Guard) Fenced() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fenced
}

// Sent says whether any attempt may have reached its upstream.
func (g *Guard) Sent() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sent
}

// AGY says whether an AGY attempt was begun for the request.
func (g *Guard) AGY() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.agy
}

// Advance lets one more AGY send go for ctx's request: for a round that
// follows a completed one with something new in it (the results of the
// searches the model asked for), which is a generation of its own, not
// the previous one sent again. Its own failure is again never re-sent.
func Advance(ctx context.Context) {
	if g := From(ctx); g != nil {
		g.mu.Lock()
		g.fenced = false
		g.mu.Unlock()
	}
}

// Fence marks the request as sent by an AGY attempt without a trace: for a
// transport that writes no HTTP headers through net/http (a test's
// RoundTripper, a plugin's), where the send is known only once it returns.
func Fence(ctx context.Context) {
	if g := From(ctx); g != nil {
		g.mu.Lock()
		g.sent, g.fenced = true, true
		g.mu.Unlock()
	}
}
