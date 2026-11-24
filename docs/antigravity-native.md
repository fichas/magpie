# Native Antigravity with the AGY profile (fork)

This fork can serve Antigravity accounts natively again, the way AsterGate's
Antigravity provider does after nwverse/astergate#3466: magpie itself owns the
Google sign-in, Code Assist project, request translation and account pool, and
an opt-in per-subscription profile sends an Anthropic Messages client's prompt
as the AGY gateway does. There is no plugin, no official CLI carrier and no
second gateway in the path.

```text
Client -> magpie gateway (auth, routing, account pick)
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

## Not ported yet (phase 2)

AsterGate's #3466 also has two dispatch guarantees for enabled accounts:

- **single send**: no provider-local retry, initial-stream failover, account
  switch or route fallback re-sends a generation once it may have been sent;
- **`Idempotency-Key` replay**: a CAS reservation before generation, and the
  completed response (≤4 MiB, 24 h) replayed with zero new usage, in shared
  Postgres.

In magpie the re-send points are spread over `forwardTranslated`'s refusal
loop (`markUnfit` and asking again), `fallback.go` and `routing.go`, and the
store would be the local SQLite (`internal/sqlitedb`) rather than Postgres.
These need their own change once the re-send points are mapped.

## Source

`internal/agycompat` carries its own NOTICE.md, LICENSE (PolyForm
Noncommercial 1.0.0 for the agy-api-gateway contributions) and MIT notice.
