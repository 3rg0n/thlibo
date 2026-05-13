// Package shellcmd parses a single-command shell string enough to
// extract its argv[0] — the program name the user / AI invoked. We
// deliberately do NOT parse full shell syntax (pipes, &&, redirects);
// the caller decides whether the string is simple enough to rewrite.
//
// This is the narrowest sufficient parser for `thlibo rewrite`.
// Full shell parsing (pipes, && chains, subshells) is deferred to
// v0.2 — not worth the ~2000 LOC of parser for a single-command
// wrap.
package shellcmd

import (
	"strings"
	"unicode"
)

// Basename returns the filename portion of argv0 without any
// extension that would confuse registry lookups:
//
//	/usr/bin/git    -> git
//	C:\\bin\\git.exe -> git
//	git             -> git
//	npm.cmd         -> npm (Windows shell-shim suffix stripped)
//
// Handles both forward- and backslash separators regardless of the
// running OS, because thlibo rewrite receives command strings from
// AI clients that may have been generated on a different OS than
// the one parsing them.
func Basename(argv0 string) string {
	if argv0 == "" {
		return ""
	}
	// Hand-rolled basename: split on the last / or \, whichever is
	// later. filepath.Base on Linux treats backslashes as literal
	// characters in filenames; on Windows it treats both as
	// separators. We want Windows behaviour everywhere.
	base := argv0
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' || base[i] == '\\' {
			base = base[i+1:]
			break
		}
	}
	// Strip common Windows shell suffixes so `git` and `git.exe`
	// collapse to the same registry key.
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	return base
}

// Argv0 returns the program name in cmd (the first word after any
// leading VAR=value env assignments), or "" if cmd is empty, contains
// shell metacharacters that would require real parsing, or is a
// bare variable assignment.
//
// Returns:
//
//	("git", true)   for "git status"
//	("git", true)   for "  git   status --short"
//	("git", true)   for "CI=1 git status"   (env prefix stripped)
//	("", false)     for "git status | grep foo"
//	("", false)     for "git status && git log"
//	("", false)     for ""
func Argv0(cmd string) (string, bool) {
	s := strings.TrimSpace(cmd)
	if s == "" {
		return "", false
	}

	// Reject anything with a shell metacharacter. We want a single
	// program invocation; anything else is out of scope for v0.1
	// rewrite. Backtick command substitution is also bailed.
	for _, r := range s {
		switch r {
		case '|', '&', ';', '>', '<', '(', ')', '$', '`':
			return "", false
		}
	}

	// Walk tokens; skip any leading VAR=value env-assignment tokens
	// (bash syntax: `CI=1 PATH=/bin/extra cmd args...`).
	for {
		tok, rest := firstToken(s)
		if tok == "" {
			return "", false
		}
		if isEnvAssignment(tok) {
			s = strings.TrimLeft(rest, " \t")
			continue
		}
		return tok, true
	}
}

// firstToken returns the first whitespace-delimited token of s along
// with the remainder of the string. Quoted tokens are unquoted.
func firstToken(s string) (tok, rest string) {
	i := 0
	for i < len(s) && isSpace(rune(s[i])) {
		i++
	}
	if i == len(s) {
		return "", ""
	}
	start := i
	for i < len(s) && !isSpace(rune(s[i])) {
		// Drop surrounding single or double quotes if they bookend
		// the token. We're not trying to be fully POSIX-correct —
		// just extracting argv[0] for the registry lookup.
		i++
	}
	tok = s[start:i]
	tok = strings.Trim(tok, `"'`)
	return tok, s[i:]
}

func isEnvAssignment(tok string) bool {
	// "FOO=bar" style. Must start with a letter/underscore, then
	// [A-Za-z0-9_]*, then '='.
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range tok[:eq] {
		if i == 0 && !(unicode.IsLetter(r) || r == '_') {
			return false
		}
		if i > 0 && !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			return false
		}
	}
	return true
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }
