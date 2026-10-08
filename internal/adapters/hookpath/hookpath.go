// Package hookpath provides path normalization utilities for hook scripts
// across multiple adapters.
package hookpath

import "strings"

// Normalise converts a Windows-style path to forward slashes.
// On non-Windows, it's a no-op. We don't rewrite the drive letter;
// Git Bash accepts both `C:/...` and `/c/...`, and Claude Code's
// Bash tool resolves `C:/...` correctly.
//
// Simple, allocation-free for the common case where no change
// is needed.
func Normalise(p string) string {
	// Simple, allocation-free for the common case where no change
	// is needed.
	if !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, "\\", "/")
}
