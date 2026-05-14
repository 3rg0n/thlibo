package processors

import "strings"

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

const (
	thoughtOpen  = "<|channel>thought"
	thoughtClose = "<channel|>"

	// maxThoughtBytes caps how much content a single thought block
	// may contain before we give up and treat the open marker as
	// literal text. Protects against a model output (or adversarial
	// tool-output leak) where a spurious open marker never closes
	// and would otherwise eat the real answer. 64 KiB is ~16k tokens,
	// well above any realistic thinking trace. See THREAT_MODEL.md
	// finding #19.
	maxThoughtBytes = 64 * 1024
)

// Strip removes all `<|channel>thought...<channel|>` blocks from s.
// Multiple blocks are all removed. Text that contains no such block
// is returned unchanged. Leading whitespace introduced by the strip
// is trimmed so the caller doesn't inherit the gap where the block
// used to be.
//
// Unclosed open markers and blocks exceeding maxThoughtBytes are
// treated as literal text rather than eating the rest of the answer.
//
// This is idempotent: Strip(Strip(s)) == Strip(s).
func Strip(s string) string {
	if !strings.Contains(s, thoughtOpen) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		// Look for the next open marker starting at i.
		rel := strings.Index(s[i:], thoughtOpen)
		if rel < 0 {
			// No more markers; flush remainder.
			b.WriteString(s[i:])
			break
		}
		// Emit anything before the open marker verbatim.
		b.WriteString(s[i : i+rel])
		openStart := i + rel
		afterOpen := openStart + len(thoughtOpen)

		// Look for the matching close marker, but only within a
		// bounded window. Scanning s[afterOpen:end] avoids letting a
		// crafted input force us to search the whole tail.
		end := afterOpen + maxThoughtBytes
		if end > len(s) {
			end = len(s)
		}
		closeRel := strings.Index(s[afterOpen:end], thoughtClose)
		if closeRel < 0 {
			// Either unclosed or the block was too large. Treat the
			// whole open marker as literal and resume scanning one byte
			// past it so we don't infinite-loop on `<|channel>thought
			// <|channel>thought...`. The test suite's "unclosed" case
			// checks this branch.
			b.WriteString(s[openStart:afterOpen])
			i = afterOpen
			continue
		}
		closeEnd := afterOpen + closeRel + len(thoughtClose)
		// Drop bytes openStart..closeEnd.
		i = closeEnd
	}
	out := b.String()
	// Trim leading whitespace introduced by a leading thought block.
	for len(out) > 0 && (out[0] == '\n' || out[0] == '\r' || out[0] == ' ' || out[0] == '\t') {
		out = out[1:]
	}
	return out
}
