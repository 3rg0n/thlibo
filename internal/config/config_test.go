package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreOffByDefault(t *testing.T) {
	d := Defaults()
	if d.AutoShorthandOnWrite {
		t.Error("auto-shorthand must default to OFF — opt-in only")
	}
	if d.AutoShorthandYAMLProse {
		t.Error("YAML-prose mode must default to OFF — opt-in only")
	}
	if d.AutoShorthandMinBytes <= 0 {
		t.Errorf("MinBytes default = %d, want > 0", d.AutoShorthandMinBytes)
	}
	if len(d.AutoShorthandPaths) == 0 {
		t.Error("default path globs should not be empty")
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("THLIBO_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	cfg := Load()
	if cfg.AutoShorthandOnWrite {
		t.Error("missing config must yield Defaults()")
	}
}

func TestLoadHonoursAutoShorthandToggle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := []byte("auto_shorthand_on_write: true\nauto_shorthand_min_bytes: 1024\n")
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THLIBO_CONFIG", cfgPath)

	cfg := Load()
	if !cfg.AutoShorthandOnWrite {
		t.Error("expected auto_shorthand_on_write=true")
	}
	if cfg.AutoShorthandMinBytes != 1024 {
		t.Errorf("min_bytes = %d, want 1024", cfg.AutoShorthandMinBytes)
	}
	// Path globs should fall back to defaults when not set in file.
	if len(cfg.AutoShorthandPaths) == 0 {
		t.Error("path globs should fall back to defaults when omitted")
	}
}

func TestLoadMalformedYAMLReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("auto_shorthand_on_write: not-a-bool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THLIBO_CONFIG", cfgPath)

	cfg := Load()
	if cfg.AutoShorthandOnWrite {
		t.Error("malformed YAML must fail-safe to Defaults() (off)")
	}
}

func TestMatchesAutoShorthandPath(t *testing.T) {
	cfg := Defaults()
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/alice/.claude/skills/foo/SKILL.md", true},
		{"C:/Dev/myrepo/.claude/skills/foo/SKILL.md", true},
		{"/Users/alice/CLAUDE.md", true},
		{"/Users/alice/projects/forging/prompts/code-review.yaml", true},
		{"/Users/alice/projects/forging/prompts/code-review.yml", true},
		// Negative cases — paths that must NOT match the defaults.
		{"/Users/alice/code/main.go", false},
		{"/Users/alice/code/README.md", false},
		{"/Users/alice/notes.md", false},
		{"/var/log/syslog", false},
	}
	for _, tc := range cases {
		got := cfg.MatchesAutoShorthandPath(tc.path)
		if got != tc.want {
			t.Errorf("MatchesAutoShorthandPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestMatchesEmptyGlobsListMatchesNothing(t *testing.T) {
	cfg := Defaults()
	cfg.AutoShorthandPaths = nil
	if cfg.MatchesAutoShorthandPath("/anything/SKILL.md") {
		t.Error("empty globs should match nothing")
	}
}

func TestGlobMatchEdgeCases(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		// ** crosses /
		{"**/foo.md", "a/b/c/foo.md", true},
		{"**/foo.md", "foo.md", true},
		{"**/foo.md", "foo.md/bar", false},
		// * does not cross /
		{"*.md", "foo.md", true},
		{"*.md", "a/foo.md", false},
		// ? single non-/
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		// Combination
		{"**/*.yaml", "x/y/z.yaml", true},
		{"**/*.yaml", "z.yaml", true},
		// Anchored both ends
		{"abc", "abcd", false},
		{"abc", "abc", true},
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.path)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}
