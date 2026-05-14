// Package execpolicy is the belt-and-suspenders check `thlibo exec`
// runs against the command it was asked to execute. Claude Code's
// own permission layer is the primary gate; this package is a
// defence-in-depth second check so a buggy AI client or a misplaced
// PreToolUse matcher can't bypass a command the user wants blocked.
//
// Policy lives at ~/.thlibo/policy.yaml (or $THLIBO_POLICY) with
// shape:
//
//	version: 1
//	allow:
//	  - git
//	  - npm
//	  - cargo
//	  - pip*           # glob: matches pip, pipx, pip-compile...
//	deny:
//	  - rm             # deny always wins over allow
//	  - "sudo*"        # any sudo invocation
//
// Semantics:
//
//  1. Deny patterns are checked first; a match returns DecisionDeny.
//  2. Allow patterns are checked next; a match returns DecisionAllow.
//  3. Falls through to DecisionDefault, configurable (allow or deny);
//     the shipped default is DecisionAllow so the absence of a policy
//     file keeps behaviour identical to v0.1.
//
// Patterns are case-sensitive on Unix and case-insensitive on
// Windows; both are matched with filepath.Match semantics on the
// basename of argv[0]. A pattern without a glob metachar is an exact
// basename compare.
//
// See THREAT_MODEL.md finding #22.
package execpolicy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decision is the result of evaluating argv0 against a Policy.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
)

// Policy is the loaded shape of policy.yaml.
type Policy struct {
	Version int      `yaml:"version"`
	Allow   []string `yaml:"allow"`
	Deny    []string `yaml:"deny"`
	// Default sets the fall-through decision when neither Allow nor
	// Deny matches. Valid values: "allow" (default), "deny".
	Default string `yaml:"default"`
}

// ErrDenied is the error returned by an execpolicy-gated code path
// when Evaluate returns DecisionDeny.
var ErrDenied = errors.New("execpolicy: command denied by policy")

// DefaultPath returns the path the policy file is loaded from.
// $THLIBO_POLICY overrides; otherwise ~/.thlibo/policy.yaml.
func DefaultPath() string {
	if p := os.Getenv("THLIBO_POLICY"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".thlibo", "policy.yaml")
}

// Load reads a Policy from path. A missing file returns an empty
// Policy (every command falls through to DecisionAllow). Parse
// errors are returned to the caller so a broken policy file fails
// loudly instead of silently opening the gate.
func Load(path string) (*Policy, error) {
	if path == "" {
		return &Policy{}, nil
	}
	buf, err := os.ReadFile(path) // #nosec G304 -- path is user-configured via THLIBO_POLICY or ~/.thlibo
	if err != nil {
		if os.IsNotExist(err) {
			return &Policy{}, nil
		}
		return nil, fmt.Errorf("execpolicy: read %s: %w", path, err)
	}
	var p Policy
	if err := yaml.Unmarshal(buf, &p); err != nil {
		return nil, fmt.Errorf("execpolicy: parse %s: %w", path, err)
	}
	if p.Version != 0 && p.Version != 1 {
		return nil, fmt.Errorf("execpolicy: unsupported version %d (expected 1)", p.Version)
	}
	return &p, nil
}

// Evaluate returns the Decision for argv0. argv0 may be a full path;
// we compare against the basename. Empty argv0 is treated as deny.
func (p *Policy) Evaluate(argv0 string) Decision {
	if argv0 == "" {
		return DecisionDeny
	}
	name := normaliseName(argv0)

	// Deny wins.
	for _, pat := range p.Deny {
		if matchPattern(name, pat) {
			return DecisionDeny
		}
	}
	// Explicit allow.
	for _, pat := range p.Allow {
		if matchPattern(name, pat) {
			return DecisionAllow
		}
	}
	// Fall-through.
	switch strings.ToLower(strings.TrimSpace(p.Default)) {
	case "deny":
		return DecisionDeny
	default:
		return DecisionAllow
	}
}

// normaliseName reduces argv0 to a comparable basename. Strips
// directories and, on Windows, the .exe suffix and case.
func normaliseName(argv0 string) string {
	name := filepath.Base(argv0)
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
		name = strings.TrimSuffix(name, ".exe")
	}
	return name
}

// matchPattern compares name against a policy pattern using
// filepath.Match semantics (on Windows, both sides are already
// lower-cased). A malformed pattern never matches — we'd rather
// fail open than have a typo create a silent deny.
func matchPattern(name, pat string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		pat = strings.ToLower(pat)
		pat = strings.TrimSuffix(pat, ".exe")
	}
	ok, err := filepath.Match(pat, name)
	if err != nil {
		return false
	}
	return ok
}
