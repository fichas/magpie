package agyjournal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxResponseBytes is the most of a response kept for replay.
const MaxResponseBytes = 4 << 20

// Retention is how long a reservation, and a completed response, is kept.
const Retention = 24 * time.Hour

// Header is the request header that names a replayable request.
const Header = "Idempotency-Key"

// ReplayedHeader marks a response given back from the journal. Only magpie
// sets it: an upstream's header of that name is never passed on.
const ReplayedHeader = "X-Magpie-Replayed"

// Error is a refusal the journal answers itself, before any generation.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Request is what a reservation is scoped to and bound by.
type Request struct {
	// Caller identifies who asked (a gateway key's digest, or this
	// computer's own caller); "" when no caller can be told, which a key
	// is refused for.
	Caller string
	// Federated: the request came through another gateway that names only
	// itself, never its callers.
	Federated bool
	// Bound is everything the key is bound to: the API, the model asked
	// for, stream or not, the session, the client hint and the exact body.
	Protocol, Model, Session, UserAgent string
	Stream                              bool
	Body                                []byte
}

type record struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	State       string `json:"state"` // pending | complete
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
	Stream      bool   `json:"stream"`
}

// Replay is a completed response to give back as it was written.
type Replay struct {
	Status      int
	ContentType string
	Body        []byte
}

// Write gives the response back to w, marked as replayed.
func (r *Replay) Write(w http.ResponseWriter) {
	w.Header().Del(ReplayedHeader)
	if r.ContentType != "" {
		w.Header().Set("Content-Type", r.ContentType)
	}
	w.Header().Set(ReplayedHeader, "true")
	w.WriteHeader(r.Status)
	w.Write(r.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Key is the request's Idempotency-Key: present says the header was sent
// at all, and err is the 400 for a malformed one (empty, repeated, longer
// than 256, or outside printable ASCII).
func Key(h http.Header) (key string, present bool, err *Error) {
	var vals []string
	for name, vs := range h {
		if strings.EqualFold(name, Header) {
			present = true
			vals = append(vals, vs...)
		}
	}
	if !present {
		return "", false, nil
	}
	if len(vals) != 1 || len(vals[0]) == 0 || len(vals[0]) > 256 || strings.IndexFunc(vals[0], func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", true, &Error{http.StatusBadRequest, "invalid Idempotency-Key: one printable ASCII value of 1 to 256 characters"}
	}
	return vals[0], true, nil
}

// Reserve looks key up for req: a completed response to replay, a refusal,
// or the reservation it just claimed for this request to be served under.
// Exactly one of the three is non-nil.
func Reserve(ctx context.Context, store Store, key string, req Request) (*Replay, *Reservation, *Error) {
	if req.Federated {
		return nil, nil, &Error{http.StatusBadRequest, "Idempotency-Key replay needs the caller's own identity; a request through another gateway names only that gateway"}
	}
	if store == nil || req.Caller == "" {
		return nil, nil, &Error{http.StatusServiceUnavailable, "Idempotency-Key replay is unavailable: no journal, or no caller identity to scope the key to"}
	}
	scope, _ := json.Marshal([]string{"agy-response-v1", req.Caller, key})
	k := digest(scope)
	fp, _ := json.Marshal([]any{req.Protocol, req.Model, req.Stream, req.Session, req.UserAgent, digest(req.Body)})
	entry := record{Version: 1, Fingerprint: digest(fp), State: "pending", Stream: req.Stream}
	raw, gen, err := store.Load(ctx, k)
	if err != nil {
		return nil, nil, &Error{http.StatusServiceUnavailable, "Idempotency-Key replay state could not be read"}
	}
	if len(raw) != 0 {
		var saved record
		if len(raw) > 2*MaxResponseBytes || json.Unmarshal(raw, &saved) != nil || saved.Version != 1 {
			return nil, nil, &Error{http.StatusServiceUnavailable, "Idempotency-Key replay state is invalid"}
		}
		if saved.Fingerprint != entry.Fingerprint {
			return nil, nil, &Error{http.StatusConflict, "Idempotency-Key is bound to a different request (content, model, session or stream)"}
		}
		if saved.State != "complete" || saved.Status < 200 || saved.Status >= 300 || len(saved.Body) > MaxResponseBytes || saved.Stream != req.Stream {
			return nil, nil, &Error{http.StatusConflict, "the request with this Idempotency-Key is running or its outcome is uncertain; it is not sent again"}
		}
		return &Replay{Status: saved.Status, ContentType: saved.ContentType, Body: saved.Body}, nil, nil
	}
	if gen != 0 {
		return nil, nil, &Error{http.StatusServiceUnavailable, "Idempotency-Key replay state is inconsistent"}
	}
	payload, _ := json.Marshal(entry)
	at := time.Now()
	won, err := store.CompareAndSwap(ctx, k, 0, payload, Retention)
	if err != nil {
		return nil, nil, &Error{http.StatusServiceUnavailable, "Idempotency-Key reservation outcome is unknown"}
	}
	if !won {
		return nil, nil, &Error{http.StatusConflict, "a request with this Idempotency-Key is already running; repeat it later to read its result"}
	}
	return nil, &Reservation{store: store, key: k, entry: entry, at: at, ctx: context.WithoutCancel(ctx)}, nil
}

// Reservation is a claimed key, served under Writer and settled by Finish.
type Reservation struct {
	store Store
	key   string
	entry record
	at    time.Time
	ctx   context.Context

	mu        sync.Mutex
	committed bool
	capture   *Capture
}

// Writer is w capturing what the request is answered with, for commit
// while applies says the key is the AGY profile's: an attempt on an
// Antigravity account with the profile on served it. A reply from any
// other provider is never kept (Finish releases its key).
func (r *Reservation) Writer(w http.ResponseWriter, applies func() bool) *Capture {
	c := &Capture{ResponseWriter: w, commit: func(status int, contentType string, body []byte) {
		if applies == nil || applies() {
			r.commit(status, contentType, body)
		}
	}}
	r.capture = c
	return c
}

// commit keeps a complete successful response: before its last frame is
// shown to a client that may close at once, and after a client gone away
// as well, since what was generated is whole.
func (r *Reservation) commit(status int, contentType string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.committed || time.Since(r.at) >= Retention || len(body) > MaxResponseBytes || status < 200 || status >= 300 {
		return
	}
	e := r.entry
	e.State, e.Status, e.ContentType, e.Body = "complete", status, contentType, body
	payload, err := json.Marshal(e)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	if ok, err := r.store.CompareAndSwap(ctx, r.key, 1, payload, Retention); ok && err == nil {
		r.committed = true
	}
}

// Finish settles the reservation once the request was served: a complete
// buffered response is committed; one that sent nothing upstream (sent
// false: no attempt's headers were written) is released, so the key can
// be used again, and so is one no AGY attempt served (applies false),
// which the key never promised anything for; anything else stays
// reserved, uncertain, until it expires.
func (r *Reservation) Finish(sent, applies bool) {
	if r.capture != nil {
		r.capture.finish()
	}
	r.mu.Lock()
	done := r.committed
	r.mu.Unlock()
	if done || (sent && applies) {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	r.store.DeleteIfGeneration(ctx, r.key, 1)
}

// Committed says whether the response was kept.
func (r *Reservation) Committed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.committed
}

// Capture is a ResponseWriter that keeps a copy of what it writes, up to
// MaxResponseBytes. An event stream is committed at its structured
// message_stop event, before that event is written on; a buffered
// response at Finish, when its body is a complete Messages reply.
type Capture struct {
	http.ResponseWriter
	commit func(status int, contentType string, body []byte)

	status   int
	body     bytes.Buffer
	scan     int
	overflow bool
	done     bool
}

func (c *Capture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
		// an upstream's marker never goes out as magpie's
		c.ResponseWriter.Header().Del(ReplayedHeader)
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *Capture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}
	if !c.done && !c.overflow {
		if c.body.Len()+len(p) > MaxResponseBytes {
			c.overflow = true
			c.body = bytes.Buffer{}
		} else {
			c.body.Write(p)
			if c.stream() && c.messageStop() {
				c.done = true
				c.commit(c.status, c.contentType(), bytes.Clone(c.body.Bytes()))
			}
		}
	}
	return c.ResponseWriter.Write(p)
}

func (c *Capture) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (c *Capture) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *Capture) contentType() string { return c.ResponseWriter.Header().Get("Content-Type") }

func (c *Capture) stream() bool {
	return strings.HasPrefix(strings.ToLower(c.contentType()), "text/event-stream")
}

// finish commits a buffered reply that is a whole Messages message.
func (c *Capture) finish() {
	if c.done || c.overflow || c.stream() {
		return
	}
	c.done = true
	var m struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(c.body.Bytes(), &m) == nil && m.Type == "message" {
		c.commit(c.status, c.contentType(), bytes.Clone(c.body.Bytes()))
	}
}

// messageStop reads the complete frames written since the last call for a
// structured message_stop event: one quoted inside generated text is in a
// delta's JSON, never the event's own type.
func (c *Capture) messageStop() bool {
	b := c.body.Bytes()
	for {
		end := bytes.Index(b[c.scan:], []byte("\n\n"))
		if end < 0 {
			return false
		}
		frame := b[c.scan : c.scan+end]
		c.scan += end + 2
		var data []byte
		for _, line := range bytes.Split(frame, []byte("\n")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
			if bytes.HasPrefix(line, []byte("data:")) {
				data = append(data, bytes.TrimSpace(line[5:])...)
				data = append(data, '\n')
			}
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &event) == nil && event.Type == "message_stop" {
			return true
		}
	}
}
