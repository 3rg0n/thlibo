package shorthandcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// buildHookOutput is a pure function — exercise it directly to
// verify it preserves unrelated tool_input fields and only
// mutates the targeted one.
func TestBuildHookOutputPreservesUnrelatedFields(t *testing.T) {
	envIn := []byte(`{
  "tool_name": "Write",
  "tool_input": {
    "file_path": "/tmp/SKILL.md",
    "content": "ORIGINAL",
    "encoding": "utf-8"
  }
}`)

	out, err := buildHookOutput(envIn, "content", "REWRITTEN")
	if err != nil {
		t.Fatalf("buildHookOutput: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse output: %v", err)
	}

	hso, ok := got["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatal("hookSpecificOutput missing")
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v", hso["hookEventName"])
	}
	if hso["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v", hso["permissionDecision"])
	}

	ui, ok := hso["updatedInput"].(map[string]any)
	if !ok {
		t.Fatal("updatedInput missing")
	}
	if ui["content"] != "REWRITTEN" {
		t.Errorf("content not rewritten: %v", ui["content"])
	}
	if ui["file_path"] != "/tmp/SKILL.md" {
		t.Errorf("file_path lost: %v", ui["file_path"])
	}
	if ui["encoding"] != "utf-8" {
		t.Errorf("unrelated field 'encoding' lost: %v", ui["encoding"])
	}
}

// pickContent prefers Write-tool content over Edit-tool new_string.
func TestPickContentPrefersWriteOverEdit(t *testing.T) {
	cases := []struct {
		content, newStr string
		wantField       string
		wantBody        string
	}{
		{"hello", "", "content", "hello"},
		{"", "world", "new_string", "world"},
		{"hello", "world", "content", "hello"}, // both -> content wins
		{"", "", "", ""},
	}
	for _, tc := range cases {
		ti := struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
			NewStr   string `json:"new_string"`
		}{Content: tc.content, NewStr: tc.newStr}
		f, b := pickContent(&ti)
		if f != tc.wantField || b != tc.wantBody {
			t.Errorf("pickContent(content=%q, new_string=%q) = (%q, %q), want (%q, %q)",
				tc.content, tc.newStr, f, b, tc.wantField, tc.wantBody)
		}
	}
}

// saveOriginal writes the body and a meta.json under the cases dir.
func TestSaveOriginal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("USERPROFILE", tmpHome) // Windows
	t.Setenv("HOME", tmpHome)        // POSIX

	body := "the original content\nwith multiple lines\n"
	if err := saveOriginal("/tmp/foo/SKILL.md", body); err != nil {
		t.Fatalf("saveOriginal: %v", err)
	}

	root := filepath.Join(tmpHome, ".thlibo", "cases", "shorthand")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir cases/shorthand: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 case dir, got %d", len(entries))
	}
	caseDir := filepath.Join(root, entries[0].Name())

	got, err := os.ReadFile(filepath.Join(caseDir, "original"))
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != body {
		t.Errorf("original mismatch:\ngot: %q\nwant: %q", got, body)
	}

	metaBytes, err := os.ReadFile(filepath.Join(caseDir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}
	if meta["target_path"] != "/tmp/foo/SKILL.md" {
		t.Errorf("target_path = %v", meta["target_path"])
	}
	if meta["sha256"] == "" || meta["sha256"] == nil {
		t.Errorf("sha256 missing")
	}
}
