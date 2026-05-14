package execpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should be no error, got %v", err)
	}
	if p.Evaluate("git") != DecisionAllow {
		t.Errorf("empty policy should allow-by-default")
	}
}

func TestLoadParsesAllowDeny(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	body := []byte("version: 1\nallow: [git, npm]\ndeny: [rm, \"sudo*\"]\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, tc := range []struct {
		argv0 string
		want  Decision
	}{
		{"git", DecisionAllow},
		{"npm", DecisionAllow},
		{"rm", DecisionDeny},
		{"sudo", DecisionDeny},
		{"sudoers", DecisionDeny}, // matches sudo*
		{"curl", DecisionAllow},   // fall-through default
	} {
		if got := p.Evaluate(tc.argv0); got != tc.want {
			t.Errorf("Evaluate(%q) = %v, want %v", tc.argv0, got, tc.want)
		}
	}
}

func TestDenyWinsOverAllow(t *testing.T) {
	p := &Policy{Allow: []string{"rm"}, Deny: []string{"rm"}}
	if p.Evaluate("rm") != DecisionDeny {
		t.Errorf("deny must win over allow when both match")
	}
}

func TestDefaultDenyBlocksUnlisted(t *testing.T) {
	p := &Policy{Default: "deny", Allow: []string{"git"}}
	if p.Evaluate("git") != DecisionAllow {
		t.Errorf("git should be explicitly allowed")
	}
	if p.Evaluate("curl") != DecisionDeny {
		t.Errorf("curl not on allow-list, default=deny => DecisionDeny")
	}
}

func TestEvaluateStripsPath(t *testing.T) {
	p := &Policy{Deny: []string{"rm"}}
	if p.Evaluate("/usr/bin/rm") != DecisionDeny {
		t.Errorf("path-prefixed argv0 should still match basename")
	}
}

func TestEmptyArgv0IsDenied(t *testing.T) {
	p := &Policy{}
	if p.Evaluate("") != DecisionDeny {
		t.Errorf("empty argv0 must be denied (safety net)")
	}
}

func TestLoadRejectsBadVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	_ = os.WriteFile(path, []byte("version: 2\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Errorf("bad version must error")
	}
}

func TestMalformedPatternNeverMatches(t *testing.T) {
	// "[" is an unterminated character class in filepath.Match, so it
	// must not silently match everything - fail closed (don't match)
	// rather than fail open (match all).
	p := &Policy{Deny: []string{"["}}
	if p.Evaluate("git") != DecisionAllow {
		t.Errorf("malformed deny pattern must not match innocent commands")
	}
}
