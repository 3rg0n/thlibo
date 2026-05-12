package shellcmd

import "testing"

func TestArgv0(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		ok    bool
	}{
		{"simple git", "git status", "git", true},
		{"extra whitespace", "  git   status --short ", "git", true},
		{"env prefix", "CI=1 git status", "git", true},
		{"multiple env prefix", "CI=1 PATH=/x git status", "git", true},
		{"quoted argv0", `"git" status`, "git", true},
		{"single-quoted argv0", `'git' status`, "git", true},
		{"pipe rejected", "git status | grep foo", "", false},
		{"and-and rejected", "git status && git log", "", false},
		{"semicolon rejected", "git status; ls", "", false},
		{"redirect rejected", "git status > out", "", false},
		{"command sub rejected", "echo $(git rev-parse HEAD)", "", false},
		{"backtick rejected", "echo `git rev-parse HEAD`", "", false},
		{"subshell rejected", "(cd repo && git status)", "", false},
		{"empty", "", "", false},
		{"whitespace only", "   \t  ", "", false},
		{"only env = no argv0", "CI=1 FOO=bar", "", false},
		{"npm", "npm install", "npm", true},
		{"cargo", "cargo test --release", "cargo", true},
		{"absolute path", "/usr/bin/git status", "/usr/bin/git", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Argv0(c.in)
			if got != c.want || ok != c.ok {
				t.Errorf("Argv0(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestBasename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"git", "git"},
		{"/usr/bin/git", "git"},
		{`C:\bin\git.exe`, "git"},
		{"git.exe", "git"},
		{"npm.cmd", "npm"},
		{"npm.CMD", "npm"},
		{"run.ps1", "run"},
		{"build.bat", "build"},
		{"", ""},
		{"./script.sh", "script.sh"}, // .sh NOT a Windows shim, keep
	}
	for _, c := range cases {
		if got := Basename(c.in); got != c.want {
			t.Errorf("Basename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsEnvAssignment(t *testing.T) {
	ok := []string{"CI=1", "A=", "PATH=/bin", "_X=y", "ABC=def", "A_B_C=x"}
	bad := []string{"", "=x", "1A=x", "cmd", "-flag=x", "FOO BAR=x"}
	for _, s := range ok {
		if !isEnvAssignment(s) {
			t.Errorf("isEnvAssignment(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if isEnvAssignment(s) {
			t.Errorf("isEnvAssignment(%q) = true, want false", s)
		}
	}
}
