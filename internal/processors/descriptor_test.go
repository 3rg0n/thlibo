package processors

import (
	"runtime"
	"strings"
	"testing"
)

func TestParseYAMLScriptBasic(t *testing.T) {
	src := `
name: git-filter
type: script
entry: run.py
match: "^On branch"
description: Compresses git status, diff, log output.
`
	d, err := ParseYAML([]byte(src), Origin{Path: "test.yaml"})
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if d.Name != "git-filter" || d.Type != KindScript || d.Entry != "run.py" {
		t.Errorf("descriptor = %+v", d)
	}
	if !d.MatchesFastPath("On branch main") {
		t.Error("fast-path regex should match")
	}
	if d.MatchesFastPath("some random output") {
		t.Error("fast-path regex should not match")
	}
}

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	src := `
name: git-filter
type: script
entry: run.py
bogus_field: 42
`
	if _, err := ParseYAML([]byte(src), Origin{Path: "t"}); err == nil {
		t.Error("expected error on unknown field")
	}
}

func TestParseYAMLRequiresEntry(t *testing.T) {
	src := `
name: bad
type: script
`
	if _, err := ParseYAML([]byte(src), Origin{Path: "t"}); err == nil {
		t.Error("script type must require entry")
	}
}

func TestParseMarkdownBasic(t *testing.T) {
	src := `---
name: casefolder
type: prompt
temperature: 0.3
match: "(?i)error:"
description: Structures stack traces.
---

You are a log analyst.
Produce a case folder.
`
	d, err := ParseMarkdown([]byte(src), Origin{Path: "t.md"})
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if d.Name != "casefolder" || d.Type != KindPrompt {
		t.Errorf("descriptor = %+v", d)
	}
	if d.Temperature == nil || *d.Temperature != 0.3 {
		t.Errorf("Temperature = %v", d.Temperature)
	}
	if !strings.Contains(d.SystemPrompt, "case folder") {
		t.Errorf("SystemPrompt = %q", d.SystemPrompt)
	}
	if !d.MatchesFastPath("error: something broke") {
		t.Error("case-insensitive regex should match")
	}
}

func TestParseMarkdownNoFrontmatterIsBody(t *testing.T) {
	src := `no frontmatter at all`
	_, err := ParseMarkdown([]byte(src), Origin{Path: "t.md"})
	// Without frontmatter we have no name, which must fail validation.
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestParseMarkdownRequiresBody(t *testing.T) {
	src := `---
name: empty
type: prompt
---
`
	if _, err := ParseMarkdown([]byte(src), Origin{Path: "t.md"}); err == nil {
		t.Error("prompt processor must have non-empty body")
	}
}

func TestValidateRejectsBadName(t *testing.T) {
	bad := []string{"", "UPPER", "has_underscore", "-leading-dash", "x/path", strings.Repeat("a", 100)}
	for _, n := range bad {
		src := "name: " + n + "\ntype: script\nentry: run.py\n"
		if _, err := ParseYAML([]byte(src), Origin{Path: "t"}); err == nil {
			t.Errorf("name %q should be rejected", n)
		}
	}
}

// C1: entry extension -> interpreter mapping.
func TestEntryCommandByExtension(t *testing.T) {
	cases := []struct {
		entry string
		bin   string
		nArgs int
	}{
		{"run.py", "python3", 1},
		{"run.sh", "bash", 1},
		{"run.exe", "", 0}, // direct exec, returned bin is full path
		{"run.bin", "", 0},
	}
	for _, c := range cases {
		d := &Descriptor{Name: "p", Type: KindScript, Entry: c.entry}
		bin, args, err := d.EntryCommand("/tmp")
		if err != nil {
			t.Errorf("%s: %v", c.entry, err)
			continue
		}
		if c.bin == "" {
			// Direct exec: bin is the joined path, args is empty.
			if !strings.HasSuffix(bin, c.entry) {
				t.Errorf("%s: bin = %q, want suffix %q", c.entry, bin, c.entry)
			}
			if len(args) != 0 {
				t.Errorf("%s: unexpected args %v", c.entry, args)
			}
		} else {
			if bin != c.bin {
				t.Errorf("%s: bin = %q, want %q", c.entry, bin, c.bin)
			}
			if len(args) != c.nArgs {
				t.Errorf("%s: %d args, want %d", c.entry, len(args), c.nArgs)
			}
		}
	}
}

func TestEntryCommandRejectsUnknownExtension(t *testing.T) {
	d := &Descriptor{Name: "p", Type: KindScript, Entry: "run.rb"}
	if _, _, err := d.EntryCommand("/tmp"); err == nil {
		t.Error("unknown extension must be rejected")
	}
}

func TestEntryCommandRejectsPromptProcessor(t *testing.T) {
	d := &Descriptor{Name: "p", Type: KindPrompt, SystemPrompt: "x"}
	if _, _, err := d.EntryCommand("/tmp"); err == nil {
		t.Error("prompt processors have no entry command")
	}
}

// C1 additional: entry must be a plain filename, not a path (no
// traversal). Enforced at validate time.
func TestEntryMustBePlainFilename(t *testing.T) {
	cases := []string{"../run.py", "sub/dir/run.py", "/abs/run.py"}
	if runtime.GOOS == "windows" {
		cases = append(cases, `sub\run.py`, `C:\run.py`)
	}
	for _, entry := range cases {
		src := "name: p\ntype: script\nentry: " + entry + "\n"
		if _, err := ParseYAML([]byte(src), Origin{Path: "t"}); err == nil {
			t.Errorf("entry %q should be rejected (path-like)", entry)
		}
	}
}

// Hybrid: both yaml and md present -> type from yaml, body from md.
// Tested end-to-end via registry_test; descriptor-level sanity here.
func TestMarkdownFrontmatterOnlyUsesYAMLFields(t *testing.T) {
	src := `---
name: hybrid
type: prompt
---
body text
`
	d, err := ParseMarkdown([]byte(src), Origin{Path: "t.md"})
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if d.SystemPrompt != "body text" {
		t.Errorf("body = %q", d.SystemPrompt)
	}
}
