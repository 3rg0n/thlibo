package processors

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeTree lays out a processors directory on disk for tests. The
// registry uses os.DirFS over the returned dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// B2: registry loads every valid processor folder; ignores folders
// with no descriptor.
func TestRegistryScanBasic(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"git-filter/processor.yaml": "name: git-filter\ntype: script\nentry: run.py\n",
		"git-filter/run.py":         "#!/usr/bin/env python3\n",
		"casefolder/processor.md":   "---\nname: casefolder\ntype: prompt\n---\nbody\n",
		"empty-folder/.keep":        "", // no descriptor -> ignored
	})
	r, warnings, err := Build(nil, os.DirFS(dir))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if r.Len() != 2 {
		t.Errorf("Len = %d, want 2 (got names %v)", r.Len(), r.Names())
	}
	if r.Get("git-filter") == nil {
		t.Error("git-filter missing")
	}
	if r.Get("casefolder") == nil {
		t.Error("casefolder missing")
	}
}

// B3: yaml + md in the same folder -> yaml wins for type; md body
// becomes the system prompt.
func TestRegistryDescriptorPrecedence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"hybrid/processor.yaml": "name: hybrid\ntype: script\nentry: run.py\n",
		"hybrid/processor.md":   "---\nname: hybrid\n---\nsystem prompt body\n",
		"hybrid/run.py":         "#!/usr/bin/env python3\n",
	})
	r, warnings, err := Build(nil, os.DirFS(dir))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("warnings: %v", warnings)
	}
	d := r.Get("hybrid")
	if d == nil {
		t.Fatal("hybrid not loaded")
	}
	if d.Type != KindScript {
		t.Errorf("type = %v, want script (yaml wins)", d.Type)
	}
	if d.SystemPrompt != "system prompt body" {
		t.Errorf("system prompt = %q", d.SystemPrompt)
	}
}

// C5: user processor with the same name overrides a builtin.
func TestRegistryUserOverridesBuiltin(t *testing.T) {
	builtin := writeTree(t, map[string]string{
		"compress/processor.md": "---\nname: compress\n---\nbuiltin body\n",
	})
	user := writeTree(t, map[string]string{
		"compress/processor.md": "---\nname: compress\n---\nuser body\n",
	})
	r, _, err := Build(os.DirFS(builtin), os.DirFS(user))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	d := r.Get("compress")
	if d == nil {
		t.Fatal("compress not loaded")
	}
	if d.Origin.Source != OriginUser {
		t.Errorf("origin = %v, want user", d.Origin.Source)
	}
	if d.SystemPrompt != "user body" {
		t.Errorf("prompt = %q, want user body", d.SystemPrompt)
	}
}

// Builtin-only load: user dir is nil.
func TestRegistryBuiltinOnly(t *testing.T) {
	builtin := writeTree(t, map[string]string{
		"compress/processor.md": "---\nname: compress\n---\nbuiltin body\n",
	})
	r, _, err := Build(os.DirFS(builtin), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1", r.Len())
	}
}

// B8g: a broken descriptor is quarantined and returned as a warning;
// the rest of the registry loads normally.
func TestRegistryQuarantinesBrokenDescriptor(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"good/processor.md":   "---\nname: good\n---\nbody\n",
		"broken/processor.md": "---\nname: UPPER-BAD\n---\nbody\n", // invalid name
	})
	r, warnings, err := Build(nil, os.DirFS(dir))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want 1", warnings)
	}
	if r.Get("good") == nil {
		t.Error("good processor should still load")
	}
	if r.Get("UPPER-BAD") != nil {
		t.Error("broken processor should be quarantined")
	}
}

// Names are returned in deterministic (alphabetical) order so the
// router's prompt to the daemon is stable across runs.
func TestRegistryNamesAreDeterministic(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"zebra/processor.md":    "---\nname: zebra\n---\nbody\n",
		"apple/processor.md":    "---\nname: apple\n---\nbody\n",
		"mango/processor.md":    "---\nname: mango\n---\nbody\n",
	})
	r, _, _ := Build(nil, os.DirFS(dir))
	got := r.Names()
	want := []string{"apple", "mango", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

// B4: fast-path regex matches dispatch the right processor, and
// non-matching input returns nil.
func TestRegistryMatchFastPath(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"git-filter/processor.yaml": "name: git-filter\ntype: script\nentry: run.py\nmatch: \"^On branch\"\n",
		"git-filter/run.py":         "",
		"cargo/processor.yaml":      "name: cargo\ntype: script\nentry: run.py\nmatch: \"^ Compiling \"\n",
		"cargo/run.py":              "",
	})
	r, _, _ := Build(nil, os.DirFS(dir))

	if d := r.MatchFastPath("On branch main\nnothing to commit\n"); d == nil || d.Name != "git-filter" {
		t.Errorf("git match: %+v", d)
	}
	if d := r.MatchFastPath(" Compiling foo v0.1.0\n"); d == nil || d.Name != "cargo" {
		t.Errorf("cargo match: %+v", d)
	}
	if d := r.MatchFastPath("plain text, no magic prefix"); d != nil {
		t.Errorf("no match expected, got %+v", d)
	}
}
