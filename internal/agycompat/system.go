// Package agycompat is the client prompt compatibility profile of the AGY
// gateway, for a native Antigravity provider: an Anthropic Messages
// client's system prompt and tool descriptions said as Antigravity's own
// client says them. It is ported from AsterGate's
// internal/adapters/provider/antigravity/agycompat, itself a Go adaptation
// of packages/agy-cli/mitm/system-policy.mjs in nwverse/agy-api-gateway at
// a854cbe7910b4a347d552562156d895a30ff2683; see NOTICE.md, LICENSE and
// MIT-NOTICE.txt here for attribution and terms.
//
// It owns no sign-in, transport, retry or authorization: the gateway's
// Code Assist route calls it on the system text and the tools' descriptions
// of a request for an account with the profile on (Provider.AgyCompat).
package agycompat

import (
	"strings"

	"github.com/yetone/magpie/internal/lazyre"
)

// Identity is Antigravity's own identity line, which a recognized client's
// identity line goes as.
const Identity = "You are Antigravity, a powerful agentic AI assistant designed by the Google Deepmind team.\nYou are working with a USER to solve their task."

const dshLine = "You are an AI agent powered by DeepSeek Harness."

var (
	billing      = lazyre.New(`(?i)^x\s*[-_]\s*(?:anthropic|antigravity|anti\s+gravity)\s*[-_]\s*billing\s*[-_]\s*header\s*:`)
	persona      = lazyre.New(`(?i)^you(?:\s+are|'re)\s+`)
	code         = lazyre.New(`(?i)\b(?:claude|antigravity|anti\s+gravity)\s+code\b`)
	agent        = lazyre.New(`(?i)\b(?:claude|antigravity|anti\s+gravity)\s+agent\b`)
	dshModel     = lazyre.New(`(?i)^you are a coding agent powered by the [a-z0-9][a-z0-9._/-]* model\.$`)
	dshUA        = lazyre.New(`(?i)^deepseek-harness/[a-z0-9][a-z0-9.+_-]*(?:\s|$)`)
	brands       = lazyre.New(`(?i)claude|anthropic`)
	toolBrands   = lazyre.New(`(?i)\b(?:claude|anthropic)\b`)
	pathToken    = lazyre.New("(?i)(?:^|[(`'\"\\[])\\.(?:claude|anthropic)\\b|\\b(?:claude|anthropic)\\.[a-z0-9]{2,}\\b")
	space        = lazyre.New(`\s+`)
	paragraphs   = lazyre.New(`\n\s*\n`)
	bullet       = lazyre.New(`^\s*[-*]\s+\S`)
	continuation = lazyre.New(`^\s+\S`)
	officialWord = lazyre.New(`(?i)\bofficial\b`)
	cliWord      = lazyre.New(`(?i)\bcli\b`)
	agentSDK     = lazyre.New(`(?i)\bagent\s+sdk\b`)
	ideWord      = lazyre.New(`\bide\b`)
	fastMode     = lazyre.New(`\bfast mode\b`)
	fastCommand  = lazyre.New(`/fast\b`)
	modelIDs     = lazyre.New(`\bmodel ids\b`)
	claudeModels = lazyre.New(`\b(?:opus|sonnet)\b`)
	agyBrand     = lazyre.New(`\b(?:antigravity|anti gravity)\b`)
)

// DSHUserAgent says whether a User-Agent is DeepSeek Harness's: a hint for
// System's generic model-identity line, never authentication or permission.
func DSHUserAgent(ua string) bool { return len(ua) <= 512 && dshUA.MatchString(ua) }

// System is text, a system or developer instruction, as the profile says
// it: a recognized client identity line as Identity, Claude Code's billing
// line left out, the client's product, fast-mode and model-id bullets and
// DeepSeek Harness's checkout and Web GUI paragraphs left out, and every
// other mention of the client's product names as Antigravity. dsh says the
// client is DeepSeek Harness (DSHUserAgent), which its own identity line
// says too.
//
// Unlike the source it leaves path-like words whole here as well as in
// tool descriptions (~/.claude/settings.json, .claude/, claude.json,
// CLAUDE.md): a system prompt names the files the agent is told to read,
// and they are on disk under those names. Running it twice changes
// nothing more.
func System(text string, dsh bool) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), dshLine) {
			dsh = true
		}
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case billing.MatchString(trimmed):
			lines[i] = ""
		case strings.EqualFold(trimmed, dshLine) ||
			(dsh && dshModel.MatchString(trimmed)) ||
			(persona.MatchString(trimmed) && ((code.MatchString(trimmed) && officialWord.MatchString(trimmed) && cliWord.MatchString(trimmed)) ||
				(agent.MatchString(trimmed) && agentSDK.MatchString(trimmed)))):
			lines[i] = Identity
		default:
			lines[i] = eachWord(line, systemWord)
		}
	}
	text = removeProductBullets(strings.Join(lines, "\n"))
	var out strings.Builder
	pos := 0
	for _, gap := range paragraphs.FindAllStringIndex(text, -1) {
		out.WriteString(dshParagraph(text[pos:gap[0]]))
		out.WriteString(text[gap[0]:gap[1]])
		pos = gap[1]
	}
	out.WriteString(dshParagraph(text[pos:]))
	return out.String()
}

// dshParagraph is part, or nothing for DeepSeek Harness's checkout and Web
// GUI paragraphs, which name the harness's own install.
func dshParagraph(part string) string {
	p := strings.ToLower(strings.TrimSpace(part))
	if strings.HasPrefix(p, "the deepseek harness implementation checkout is at ") ||
		strings.HasPrefix(p, "the runtime context names the deepseek harness implementation checkout.") ||
		strings.HasPrefix(p, "you are interacting with the user through the deepseek harness web gui at ") ||
		strings.HasPrefix(p, "you are interacting with the user through the deepseek harness web gui whose url the runtime context names.") {
		return ""
	}
	return part
}

// removeProductBullets is text without the client's product bullets (its
// CLI, desktop app and IDE), its fast-mode bullet and its model-id bullet,
// each with the lines that continue it.
func removeProductBullets(text string) string {
	lines := strings.SplitAfter(text, "\n")
	var out strings.Builder
	for i := 0; i < len(lines); {
		if !bullet.MatchString(lines[i]) {
			out.WriteString(lines[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(lines) && continuation.MatchString(lines[i]) && !bullet.MatchString(lines[i]) {
			i++
		}
		block := strings.Join(lines[start:i], "")
		n := strings.ToLower(space.ReplaceAllString(block, " "))
		product := code.MatchString(n) && cliWord.MatchString(n) && strings.Contains(n, "desktop app") && ideWord.MatchString(n)
		fast := fastMode.MatchString(n) && fastCommand.MatchString(n) && agyBrand.MatchString(n)
		models := modelIDs.MatchString(n) && agyBrand.MatchString(n) && claudeModels.MatchString(n)
		if !product && !fast && !models {
			out.WriteString(block)
		}
	}
	return out.String()
}

// ToolDescription is a tool's description with standalone product names
// said as Antigravity; paths, URLs and file names stay as written. A
// tool's name and schema are never its input.
func ToolDescription(text string) string { return eachWord(text, toolWord) }

// eachWord is text with each run of non-space f says, and its white space
// as it was.
func eachWord(text string, f func(string) string) string {
	var out strings.Builder
	pos := 0
	for _, gap := range space.FindAllStringIndex(text, -1) {
		out.WriteString(f(text[pos:gap[0]]))
		out.WriteString(text[gap[0]:gap[1]])
		pos = gap[1]
	}
	out.WriteString(f(text[pos:]))
	return out.String()
}

// pathLike is a word that names a file, folder or URL.
func pathLike(word string) bool {
	return strings.ContainsAny(word, `/\`) || pathToken.MatchString(word)
}

// systemWord is a word of a system prompt: every product name in it as
// Antigravity, as the source has it (ClaudeCode too), unless it is a path.
func systemWord(word string) string {
	if pathLike(word) {
		return word
	}
	return brands.ReplaceAllString(word, "Antigravity")
}

// toolWord is a word of a tool's description: only a standalone product
// name as Antigravity.
func toolWord(word string) string {
	if pathLike(word) {
		return word
	}
	return toolBrands.ReplaceAllString(word, "Antigravity")
}
