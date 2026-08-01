package middleware

import (
	"strings"
	"testing"
	"unicode"
)

// estimateTokens approximates a BPE token count for text.
//
// Why this exists: thlibo's value proposition is denominated in *tokens*
// sent to the frontier model, but TestTokenSavingsTable historically
// reported raw byte counts. Bytes are not a neutral proxy — they are a
// biased one. Compressed output is systematically *denser* per byte than
// its input: filters strip whitespace, indentation, and tree-drawing
// runs (all cheap in tokens) while retaining identifiers, paths, and
// error codes (all expensive). So a byte ratio flatters the result, and
// the flattery grows with how much whitespace the filter removed.
//
// This is an estimate, not a tokenizer. thlibo has no Gemma vocabulary
// available at test time and shelling out to one would make the savings
// table depend on a model download. The model is:
//
//   - a run of letters/digits costs 1 token per 4 chars, rounded up —
//     short words are one token, long identifiers and paths split into
//     several, which is BPE's dominant behaviour on code-shaped text
//   - each non-alphanumeric, non-space rune costs 1 token — punctuation
//     rarely merges in BPE vocabularies
//   - a run of whitespace costs 1 token regardless of length — the
//     single most important divergence from bytes, since indentation and
//     box-drawing padding are nearly free in tokens but expensive in
//     bytes
//
// Absolute numbers from this are not publishable as token counts. The
// figure that IS meaningful is the *ratio* between two texts measured the
// same way, and specifically whether it disagrees with the byte ratio.
// Treat a large gap as the signal: it means a filter is winning on
// whitespace rather than on content.
func estimateTokens(s string) int {
	total := 0
	runeLen := 0 // length of the alphanumeric run in progress
	inSpace := false

	flushWord := func() {
		if runeLen > 0 {
			total += (runeLen + 3) / 4 // ceil(runeLen/4)
			runeLen = 0
		}
	}

	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			flushWord()
			if !inSpace {
				total++ // one token per whitespace run, not per byte
				inSpace = true
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			inSpace = false
			runeLen++
		default:
			flushWord()
			inSpace = false
			total++ // punctuation: one each
		}
	}
	flushWord()
	return total
}

// The estimator's load-bearing properties — the ones the savings table
// relies on. Not its absolute accuracy, which is explicitly approximate.
func TestEstimateTokens(t *testing.T) {
	t.Run("whitespace runs cost one token, not one per byte", func(t *testing.T) {
		tight := estimateTokens("a b")
		padded := estimateTokens("a" + strings.Repeat(" ", 200) + "b")
		if tight != padded {
			t.Errorf("indentation should be near-free: %d vs %d", tight, padded)
		}
	})

	t.Run("long identifiers cost more than short words", func(t *testing.T) {
		short := estimateTokens("a b c d")
		long := estimateTokens("internal/pkg/subpkg/very/deep/path/to/file_07.go")
		if long <= short {
			t.Errorf("a long path should out-cost four short words: %d vs %d", long, short)
		}
	})

	t.Run("empty and whitespace-only", func(t *testing.T) {
		if got := estimateTokens(""); got != 0 {
			t.Errorf("empty = %d, want 0", got)
		}
		if got := estimateTokens("   \n\t "); got != 1 {
			t.Errorf("one whitespace run = %d, want 1", got)
		}
	})

	t.Run("monotonic in appended content", func(t *testing.T) {
		base := "error: something failed at line 42"
		if estimateTokens(base+" and more content here") <= estimateTokens(base) {
			t.Error("appending content must not lower the estimate")
		}
	})

	t.Run("bytes overstate savings on whitespace-heavy input", func(t *testing.T) {
		// The exact bias this estimator exists to expose: a filter that
		// strips only indentation shows a big byte win and no token win.
		raw := strings.Repeat("        indented line of code\n", 50)
		stripped := strings.Repeat("indented line of code\n", 50)
		byteWin := 1 - float64(len(stripped))/float64(len(raw))
		tokWin := 1 - float64(estimateTokens(stripped))/float64(estimateTokens(raw))
		if byteWin <= tokWin {
			t.Errorf("expected bytes to overstate the win: bytes=%.3f tokens=%.3f", byteWin, tokWin)
		}
	})
}
