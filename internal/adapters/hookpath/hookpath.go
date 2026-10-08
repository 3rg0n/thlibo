// Package hookpath holds the path handling the four hook adapters share.
package hookpath

import "strings"

// Normalise converts backslashes to forward slashes, on every OS. We don't
// rewrite the drive letter; Git Bash accepts both `C:/...` and `/c/...`,
// and Claude Code's Bash tool resolves `C:/...` correctly.
//
// Allocation-free for the common case where no change is needed.
func Normalise(p string) string {
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
