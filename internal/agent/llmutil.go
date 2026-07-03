package agent

import "strings"

// StripThink removes reasoning content wrapped in <think>...</think>
// tags from model output. Reasoning models (DeepSeek-R1, QwQ, etc.)
// emit chain-of-thought in these tags; only the final answer after
// the closing tag is useful for downstream consumption (user-facing
// display, summary persistence, LLM re-input as prior context).
//
// Returns the input unchanged when no <think>...</think> pair is
// present, so non-reasoning model output is unaffected. A lone
// opening <think> with no closing tag is also passed through
// unchanged (the reasoning is incomplete; better to surface than
// silently drop).
func StripThink(s string) string {
	before, after, ok := strings.Cut(s, "<think>")
	if !ok {
		return s
	}
	_, after, ok = strings.Cut(after, "</think>")
	if !ok {
		return s
	}
	return strings.TrimSpace(before + after)
}
