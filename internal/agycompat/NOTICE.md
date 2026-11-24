# Source and scope

`system.go` is ported from AsterGate's
`internal/adapters/provider/antigravity/agycompat` (nwverse/astergate, merged
in #3466 on 2026-11-20), which is a Go adaptation of
`packages/agy-cli/mitm/system-policy.mjs` in
[nwverse/agy-api-gateway](https://github.com/nwverse/agy-api-gateway), revision
`a854cbe7910b4a347d552562156d895a30ff2683`. That distribution offers its own
contributions under PolyForm Noncommercial 1.0.0; see [LICENSE](LICENSE).
Pre-existing MIT rights are retained in [MIT-NOTICE.txt](MIT-NOTICE.txt).
These notices apply to this adapted package only, not as a relicensing of
the rest of magpie, which is MIT.

Upstream credits bridge snapshot `e6ee0456` and the `agy-cli` plugin from
[magpie-community/plugins](https://github.com/magpie-community/plugins),
baseline `09e59709963af9088cf0e5307247ef40ec390f33` (0.1.49), tracing to
[DylanLIiii/opencode-antigravity-bridge](https://github.com/DylanLIiii/opencode-antigravity-bridge)
and [gitxm/yce-magpie-agy](https://github.com/gitxm/yce-magpie-agy).

One change from AsterGate's copy: product names inside path-like words of
a system prompt (`~/.claude/settings.json`, `.claude/`, `claude.json`) stay
as written, as they already did in tool descriptions.

This package owns no sign-in, transport, retry, replay or authorization.
