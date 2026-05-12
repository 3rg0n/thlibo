package processors

import "regexp"

// Gemma 4 emits a thought block before every answer, even when
// thinking is disabled (the block is empty in that case). Reference:
// https://ai.google.dev/gemma/docs/core/model_card_4 §Thinking Mode.
//
// Block shape observed in practice (documented in the capability
// guides):
//
//	<|channel>thought
//	<reasoning text>
//	<channel|>
//	<answer>
//
// With thinking disabled on E2B/E4B:
//
//	<|channel>thought
//	<channel|>
//	<answer>
//
// Our middleware strips every such block from prompt-processor
// responses before returning to the AI client. Script processors are
// unaffected - they don't see model output.
var thoughtBlockRE = regexp.MustCompile(`(?s)<\|channel>thought.*?<channel\|>`)

// Strip removes all `<|channel>thought...<channel|>` blocks from s.
// Multiple blocks are all removed. Text that contains no such block
// is returned unchanged. Leading and trailing whitespace introduced
// by the strip is trimmed so the caller doesn't inherit the gap where
// the block used to be.
//
// This is idempotent: Strip(Strip(s)) == Strip(s).
func Strip(s string) string {
	out := thoughtBlockRE.ReplaceAllString(s, "")
	// Trim only the head: leading whitespace is the most common
	// artefact after removing a leading thought block, and it's the
	// part most likely to confuse a downstream consumer. Trailing
	// whitespace the model produced intentionally stays.
	for len(out) > 0 && (out[0] == '\n' || out[0] == '\r' || out[0] == ' ' || out[0] == '\t') {
		out = out[1:]
	}
	return out
}
