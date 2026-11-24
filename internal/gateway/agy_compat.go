package gateway

import (
	"slices"

	"github.com/yetone/magpie/internal/agycompat"
	"github.com/yetone/magpie/internal/provider"
)

// The AGY prompt profile on Antigravity's built-in subscription
// (Provider.AgyCompat, `magpie provider set antigravity prompt=agy`): an
// Anthropic Messages client's system prompt and tool descriptions said as
// Antigravity's own client says them (internal/agycompat), as AsterGate's
// native Antigravity provider does for an account with agy_gateway_compat.
// The account's sign-in, project, envelope and transport are the
// built-in's, unchanged. Its rewording of the lines Google turns away runs
// after, as for every Antigravity request; a prompt sent as written
// (PromptAsWritten) is never given the profile.

// agyCompatFor says whether r, about to go to p, gets the profile: an
// Antigravity account of magpie's own (not a plugin's) with it on, and a
// request that came in on Anthropic's Messages API. Chat, Responses and
// Gemini clients go as by default, as in AsterGate.
func agyCompatFor(p provider.Provider, r *Request) bool {
	return p.AgyCompat && !p.PromptAsWritten && !p.IsPlugin() &&
		accountAgent(p) == "antigravity" && r.from == provider.Anthropic
}

// agyCompat is a copy of r with its system prompt and its tools'
// descriptions as the profile says them; dsh is DeepSeek Harness's
// User-Agent. Tool names, schemas and the conversation are left as they
// are, and r itself is not changed: the caller's request keeps the prompt
// as written, which a refusal is told against.
func agyCompat(r *Request, dsh bool) *Request {
	rc := *r
	rc.System = agycompat.System(r.System, dsh)
	if len(r.Tools) > 0 {
		rc.Tools = slices.Clone(r.Tools)
		for i := range rc.Tools {
			if rc.Tools[i].Description != "" {
				rc.Tools[i].Description = agycompat.ToolDescription(rc.Tools[i].Description)
			}
		}
	}
	return &rc
}
