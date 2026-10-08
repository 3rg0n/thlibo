package processors

import (
	"regexp"
	"strings"
)

// splitLines splits raw bytes on \n after normalising \r\n to \n,
// and drops the trailing empty element that results from a trailing newline
// (matching Python's str.splitlines() semantics).
func splitLines(raw []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	// splitlines() semantics: a trailing newline shouldn't yield a final
	// empty element. Python's str.splitlines() drops the trailing "".
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
