# Native Antigravity with the AGY profile (fork)

This fork can serve Antigravity accounts natively again, the way AsterGate's
Antigravity provider does after nwverse/astergate#3466: magpie itself owns the
Google sign-in, Code Assist project, request translation and account pool, and
an opt-in per-subscription profile sends an Anthropic Messages client's prompt
as the AGY gateway does. There is no plugin, no official CLI carrier and no
second gateway in the path.

```text
Client -> magpie gateway (auth, routing, account pick; guard + journal)
       -> built-in Antigravity: OAuth, project, Code Assist envelope
          (internal/provider/google.go, internal/gateway/codeassist.go)
       -> cloudcode-pa / daily-cloudcode-pa
```

Upstream magpie retired this route on 11-15 because Google may suspend an
account it sees on Code Assist outside Antigravity (see AGENTS.md). Turning it
on here takes that risk for the accounts it serves. It is off by default.

## Turning it on

```sh
magpie accounts antigravity-builtin on    # settings.json antigravityBuiltin, this computer only
magpie accounts add antigravity           # Google sign-in with Antigravity's OAuth client
# or: magpie accounts import antigravity <CLIProxyAPI or Cockpit file>
magpie provider set antigravity prompt=agy
```

`MAGPIE_ANTIGRAVITY_BUILTIN=1|0` overrides the saved choice for one process.

With the switch on, `provider.BuiltinRetired("antigravity")` is false, so
`googleLogins` lists the saved accounts and the gateway sends them requests.
magpie's own hourly mover (`MoveRetiring`, `HandOver`) leaves the subscription
alone; a move the user made before, or makes from the Providers page, still
stands (`Moved` wins). Turning it off retires the built-in again, accounts kept.

## The AGY prompt profile (`prompt=agy`)

`Provider.AgyCompat`, copied to every account like `PromptAsWritten`. For a
request that came in on Anthropic Messages to a built-in Antigravity account
(`agyCompatFor`), `codeAssistBodyIn` passes the system prompt and each tool's
description through `internal/agycompat` before magpie's usual rewording of
the lines Google turns away (`antigravityCloak`, `antigravityReword`):

- Claude Code's, the Claude Agent SDK's and DeepSeek Harness's identity lines
  become Antigravity's identity; a `deepseek-harness/…` User-Agent also marks
  DSH's generic model line as its own;
- the billing line, the client's product / fast-mode / model-id bullets and
  DSH's checkout and Web GUI paragraphs are left out;
- other product names say Antigravity, except inside path-like words
  (`~/.claude/settings.json`, `CLAUDE.md`, URLs). AsterGate's copy rewrote
  paths in the system prompt; this one does not.

Tool names, schemas, user turns and tool results are never rewritten. Chat,
Responses and Gemini clients, plugin-served accounts and `prompt=as-written`
are unchanged. `prompt=as-written`, `prompt=agy` and `prompt=default` are one
setting with three values.

Turning the profile on or off changes the prompt prefix, so an ongoing
session's prompt cache misses once.

## Single send

Every Anthropic Messages request is served under an `agyjournal.Guard`
(`serveMessages`, `internal/gateway/agy_dispatch.go`). An attempt on an
account with the profile on is marked (`agyjournal.Attempt`, in
`forwardTranslated`). Once such an attempt's headers are written upstream
(`httptrace.WroteHeaders`), or it got any answer, the request is *fenced*:

- `forwardOnce` refuses every further send for it (`ErrNoResend`), whatever
  route asks: the hard floor under everything below;
- `forward`'s refits, `forwardTranslated`'s re-ask loop, `translate`'s
  thinking re-ask, `streamTranslated`'s reloop / empty-reply / void-room
  re-asks return the fenced attempt's own answer instead of asking again;
- `serve`'s candidate loop treats the fenced try as its last (`agyFenced`):
  no other account, member, replan, patience round, credits or quota retry,
  and no slow-first-token cut (`holdWriter` never marks a fenced try slow).
  The client gets Google's own failure, told as a last try's is;
- below magpie's code, the transport sends it once: no `X-Idempotency-Key`
  mark (so Go never replays it on a reused connection), no `replayable`
  retry of an "unsent" request (unsent doesn't hold over TLS), no second
  upload after a lost HTTP/2 ping (`drawsApart`), and no redirect followed
  (`provider.Keyed`).

An attempt that failed before its headers went (DNS, dial, TLS, a proxy
refusing) sent nothing: the fence stays down and fallback works as before.
A search or advisor round that follows a completed round is a generation of
its own (`agyjournal.Advance`), sent once as well. Requests without the
profile, and other APIs' clients, keep magpie's retries.

## `Idempotency-Key` replay

While the built-in is on (`settings.AntigravityBuiltin`), a Messages request
with an `Idempotency-Key` (one printable-ASCII value, 1–256 characters; else
400) is reserved before anything is asked, in `agy-journal.db` beside
`providers.json` (SQLite, WAL, one statement per compare-and-swap, so two
magpie processes never both win a key):

- the key is scoped to the caller: on this computer the token it sent
  (any, as loopback takes any), from another computer its gateway key; a
  request with neither, or one passed on by another magpie, which names
  only itself, is refused (503 / 400);
- it is bound to the model asked for, stream or not, the session, the
  agent's User-Agent and the exact body: another request under it is 409;
- the reply is captured outside every writer `serve` adds (member names,
  unmasking), so what is kept is exactly what the client got. A stream is
  committed at its structured `message_stop`, before that frame is written
  on; a buffered reply when it is a whole `message`; at most 4 MiB, kept
  24 h, and only if an AGY attempt served it;
- a repeat gets those bytes and their Content-Type back with
  `X-Magpie-Replayed: true` (an upstream's header of that name is dropped),
  and no provider is asked, so no usage is counted;
- while it runs, or after an outcome that may have reached Google and
  wasn't a complete success (an error, a cut stream, a client gone), a
  repeat is 409 until the record expires. Only a request that sent nothing
  upstream, or that no AGY attempt served, releases its key.

There is no takeover of a running request, no partial or error replay, and
no promise past retention.

## Not ported

AsterGate's Postgres store and cross-gateway federation semantics: magpie
keeps the journal locally; requests through a Remote magpie can't use keys.

## Source

`internal/agycompat` carries its own NOTICE.md, LICENSE (PolyForm
Noncommercial 1.0.0 for the agy-api-gateway contributions) and MIT notice.
