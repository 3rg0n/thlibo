package pdf

// thlibo: local addition. Upstream has no lexer test of its own; this one
// exists for a specific defect class rather than for coverage.

import (
	"strings"
	"testing"
)

// TestNextTokenAlwaysAdvances is the regression test for the stray-delimiter
// hang found by FuzzParseInlineDict on the input ")".
//
// NextToken dispatches `)`, `{`, and `}` to readKeyword — none has a case of
// its own — and readKeyword's scan loop stopped on a delimiter immediately,
// returning a zero-length keyword without advancing pos. Every caller in
// this package loops on NextToken until EOF or error, so a single stray
// delimiter anywhere in a content stream meant page text extraction never
// returned.
//
// A hang is worse than a crash here: thlibo's fail-open contract recovers a
// panic and passes the original bytes through, but nothing recovers a hang —
// the hook blocks and the AI client waits on it. So the invariant under test
// is progress, not correctness: whatever NextToken decides a byte is, it must
// consume something.
func TestNextTokenAlwaysAdvances(t *testing.T) {
	inputs := []string{
		")",         // the fuzz-minimised case: close paren, no open
		"{",         // PostScript function braces leaking into an object
		"}",         //
		"/A ) /B 1", // a stray delimiter mid-dict, where resync matters
		"} { ) } {",
		"BT )( ET",
		">", // lone '>' — errors rather than hangs, but must not loop
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			lex := NewLexer([]byte(in))
			last := -1
			for i := 0; i <= len(in)+2; i++ {
				tok, err := lex.NextToken()
				if err != nil || tok.Type == TEOF {
					return
				}
				if lex.pos <= last {
					t.Fatalf("position did not advance (pos=%d, token %v %q)",
						lex.pos, tok.Type, tok.Str)
				}
				last = lex.pos
			}
			t.Fatalf("still tokenising %d-byte input after %d tokens", len(in), len(in)+3)
		})
	}
}

// A stray delimiter must not swallow the tokens after it: the parser
// resynchronises on the next real token, which is what keeps a malformed
// operand from misaligning the whole operator stream.
func TestNextTokenResynchronisesAfterStrayDelimiter(t *testing.T) {
	lex := NewLexer([]byte("/MCID ) 7"))
	var got []string
	for {
		tok, err := lex.NextToken()
		if err != nil || tok.Type == TEOF {
			break
		}
		if tok.Type == TKeyword && tok.Str == "" {
			continue // the stray delimiter itself
		}
		got = append(got, tok.Str)
	}
	if want := "MCID 7"; strings.Join(got, " ") != want {
		t.Errorf("tokens = %q, want %q", strings.Join(got, " "), want)
	}
}
