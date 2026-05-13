// Package promptsan escapes Gemma 4 control markers that might appear
// in untrusted tool output before it is handed to the model. Gemma's
// tokenizer treats sequences like `<|tool_call>`, `<tool_call|>`,
// `<|"|>`, `<|channel|>`, and `<|turn>` as special tokens. If a
// malicious `git diff` or `npm list` contains them verbatim, the model
// can interpret the bytes as control rather than data, which can shift
// routing decisions or leak prompt structure.
//
// The router is already GBNF-constrained, so the worst-case routing
// attack is a wrong-but-valid processor chain. The real blast radius is
// the prompt-processor user turn (compress, casefolder), which is
// free-form. Running Sanitize on both turns closes the marker path
// without any quality cost - these byte sequences do not occur in
// real-world git/npm/cargo output.
//
// The escape strategy is: insert a U+200B ZERO WIDTH SPACE between `<`
// and `|`, and between `|` and `>`. This breaks the tokenizer's
// two-byte lookahead that would otherwise fuse them into a control
// token, and it is invisible in every terminal and renderer. Regular
// content containing `<` or `|` or `>` individually is untouched.
package promptsan

import "strings"

// Zwsp is U+200B ZERO WIDTH SPACE. Invisible, cannot be part of any
// Gemma special token, survives JSON transport. Built via rune
// conversion so the source file stays ASCII-clean.
var Zwsp = string(rune(0x200b))

// Sanitize returns s with Gemma marker prefixes (`<|`) and suffixes
// (`|>`) broken up by a zero-width space, so the model's tokenizer
// cannot fuse them into a control token. All other bytes pass through
// unchanged.
func Sanitize(s string) string {
	if s == "" || (!strings.Contains(s, "<|") && !strings.Contains(s, "|>")) {
		return s
	}
	s = strings.ReplaceAll(s, "<|", "<"+Zwsp+"|")
	s = strings.ReplaceAll(s, "|>", "|"+Zwsp+">")
	return s
}
