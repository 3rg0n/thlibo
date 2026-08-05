package shorthandcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/config"
)

// RunWriteHook is the one thlibo code path that rewrites files the user
// authored, so its bail-out gates are the load-bearing part: each is a
// promise that a given class of write is left alone. Silence on stdout is
// what tells Claude Code to keep the original tool_input, so every test
// below asserts empty output — a stray byte there is a rewrite.
//
// Everything up to buildEngine() is deterministic ($THLIBO_CONFIG is
// honoured by config.Load), which is exactly the set of gates worth
// pinning. Past that point the engine needs a reachable daemon, so the
// rewrite path is covered in internal/shorthand where the backend is
// injectable.

// writeHookConfig writes a config.yaml and points $THLIBO_CONFIG at it,
// with HOME isolated so saveOriginal cannot touch the real case history.
func writeHookConfig(t *testing.T, yaml string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THLIBO_CONFIG", cfg)
}

// runWriteHook feeds envelope on stdin and returns (exit code, stdout).
// Real *os.File on both ends: RunWriteHook reads os.Stdin and writes
// os.Stdout directly.
func runWriteHook(t *testing.T, envelope string) (int, string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString(envelope)
		_ = w.Close()
	}()

	outPath := filepath.Join(t.TempDir(), "out")
	outF, err := os.Create(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = r, outF
	code := RunWriteHook(nil)
	os.Stdin, os.Stdout = origIn, origOut

	_ = r.Close()
	_ = outF.Close()
	got, err := os.ReadFile(outPath) // #nosec G304 -- t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return code, string(got)
}

const enabledConfig = "auto_shorthand_on_write: true\nauto_shorthand_min_bytes: 100\n"

// Guard the fixture, not just the behaviour: if $THLIBO_CONFIG were
// ignored, enabledConfig would silently be the off-by-default config and
// every "enabled but bailed for reason X" case below would pass because
// the feature was off — proving nothing about reason X.
func TestEnabledConfigActuallyEnables(t *testing.T) {
	writeHookConfig(t, enabledConfig)

	cfg := config.Load()
	if !cfg.AutoShorthandOnWrite {
		t.Fatal("$THLIBO_CONFIG was not honoured; the enabled-config test cases would pass vacuously")
	}
	if cfg.AutoShorthandMinBytes != 100 {
		t.Errorf("min_bytes = %d, want 100 from the fixture", cfg.AutoShorthandMinBytes)
	}
	// And the off case really is off, so the two are distinguishable.
	writeHookConfig(t, "{}\n")
	if config.Load().AutoShorthandOnWrite {
		t.Error("auto_shorthand_on_write defaulted to true; it must be opt-in")
	}
}

// Every bail-out gate: exit 0 with empty stdout, so Claude Code writes
// the user's original bytes. Table-driven because the gates are
// independent branches of one function and each is a separate promise.
func TestWriteHookBailsOutSilently(t *testing.T) {
	body := strings.Repeat("some prose that is long enough to clear the floor. ", 20)

	for _, tc := range []struct {
		name     string
		config   string
		env      map[string]string
		envelope string
	}{
		{
			// The documented default. Auto-rewriting a user's source
			// files without opt-in would be the worst failure here, so
			// this gate matters more than any other.
			name:     "feature off by default",
			config:   "{}\n",
			envelope: `{"tool_input":{"file_path":"/tmp/notes.md","content":"` + body + `"}}`,
		},
		{
			// THLIBO_DISABLED is checked before config is loaded: the
			// kill switch has to work when config itself is broken.
			name:     "THLIBO_DISABLED=1",
			config:   enabledConfig,
			env:      map[string]string{"THLIBO_DISABLED": "1"},
			envelope: `{"tool_input":{"file_path":"/tmp/notes.md","content":"` + body + `"}}`,
		},
		{
			name:     "malformed envelope",
			config:   enabledConfig,
			envelope: `{"tool_input":`,
		},
		{
			name:     "neither content nor new_string",
			config:   enabledConfig,
			envelope: `{"tool_input":{"file_path":"/tmp/notes.md"}}`,
		},
		{
			// No path means no glob check is possible, so rewriting
			// would bypass the user's path allowlist entirely.
			name:     "empty file_path",
			config:   enabledConfig,
			envelope: `{"tool_input":{"file_path":"","content":"` + body + `"}}`,
		},
		{
			// A .go file is not in the default allowlist. Shorthand is
			// prose compression; applying it to source would corrupt it.
			name:     "path outside the allowlist",
			config:   enabledConfig,
			envelope: `{"tool_input":{"file_path":"/tmp/main.go","content":"` + body + `"}}`,
		},
		{
			name:     "body under min_bytes",
			config:   "auto_shorthand_on_write: true\nauto_shorthand_min_bytes: 100000\n",
			envelope: `{"tool_input":{"file_path":"/tmp/notes.md","content":"` + body + `"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeHookConfig(t, tc.config)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			code, out := runWriteHook(t, tc.envelope)
			if code != ExitOK {
				t.Errorf("exit = %d, want ExitOK(%d) — a non-zero exit surfaces as a hook error in the client", code, ExitOK)
			}
			if out != "" {
				t.Errorf("wrote %d bytes to stdout; a bail-out must emit nothing or the client substitutes it:\n%s", len(out), out)
			}
		})
	}
}

// The kill switch accepts the same truthy spellings as every other
// thlibo hook. A user who set THLIBO_DISABLED=true and still got their
// files rewritten would have no reason to suspect the spelling.
func TestWriteHookKillSwitchSpellings(t *testing.T) {
	body := strings.Repeat("prose prose prose prose prose prose. ", 30)
	envelope := `{"tool_input":{"file_path":"/tmp/notes.md","content":"` + body + `"}}`

	for _, v := range []string{"1", "true", "yes", "on"} {
		t.Run(v, func(t *testing.T) {
			writeHookConfig(t, enabledConfig)
			t.Setenv("THLIBO_DISABLED", v)

			if code, out := runWriteHook(t, envelope); code != ExitOK || out != "" {
				t.Errorf("THLIBO_DISABLED=%q: exit=%d out=%q, want 0 and empty", v, code, out)
			}
		})
	}
}

// A bail-out must also leave no trace on disk: saveOriginal runs only on
// the rewrite path, so a case dir appearing here would mean the gates
// were evaluated in the wrong order.
func TestWriteHookBailOutWritesNothingToDisk(t *testing.T) {
	writeHookConfig(t, "{}\n") // feature off
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("prose that clears any floor. ", 40)
	runWriteHook(t, `{"tool_input":{"file_path":"/tmp/notes.md","content":"`+body+`"}}`)

	if _, err := os.Stat(filepath.Join(home, ".thlibo")); !os.IsNotExist(err) {
		t.Errorf("bail-out created ~/.thlibo (err=%v); nothing should be saved when no rewrite happens", err)
	}
}

// looksLikeYAMLPath: extension wins, then content sniff. It gates the
// YAML walker, which rewrites selected scalars — misfiring it on
// non-YAML prose is the semantic risk that makes YAML mode opt-in.
func TestLooksLikeYAMLPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
		want bool
	}{
		{"yaml extension", "/tmp/a.yaml", "just prose here", true},
		{"yml extension", "/tmp/a.yml", "just prose here", true},
		{"markdown with prose", "/tmp/a.md", "just prose here", false},
		// No extension: the content sniff is the only signal.
		{"no extension, yaml body", "/tmp/rules", "key: value\nother: thing\n", true},
		{"no extension, prose body", "/tmp/rules", "just prose here", false},
		{"empty path and body", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeYAMLPath(tc.path, tc.body); got != tc.want {
				t.Errorf("looksLikeYAMLPath(%q, %q) = %v, want %v", tc.path, tc.body, got, tc.want)
			}
		})
	}
}

// shouldUseYAMLMode is the CLI counterpart. mode=on/off must be absolute
// — they are the user's explicit override, and an extension or content
// sniff that outvoted them would make the flag useless.
func TestShouldUseYAMLMode(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mode, path, body string
		want             bool
	}{
		{"on forces yaml on prose", "on", "/tmp/a.md", "just prose", true},
		{"off forces flat on a .yaml file", "off", "/tmp/a.yaml", "key: value\n", false},
		{"auto follows the extension", "auto", "/tmp/a.yaml", "just prose", true},
		{"auto follows .yml", "auto", "/tmp/a.yml", "just prose", true},
		{"auto sniffs content for other extensions", "auto", "/tmp/a.md", "key: value\nk2: v2\n", true},
		{"auto says no on plain prose", "auto", "/tmp/a.md", "just prose", false},
		// "-" is the stdin convention and must not be treated as a
		// filename — an extension check on it is meaningless.
		{"stdin falls through to the content sniff", "auto", "-", "key: value\nk2: v2\n", true},
		{"stdin with prose", "auto", "-", "just prose", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUseYAMLMode(tc.mode, tc.path, tc.body); got != tc.want {
				t.Errorf("shouldUseYAMLMode(%q, %q, %q) = %v, want %v", tc.mode, tc.path, tc.body, got, tc.want)
			}
		})
	}
}

// readInput: "-" is stdin, anything else is a file, and a missing file
// is an error rather than empty bytes — empty content would look like a
// legitimately empty file and get compressed to nothing.
func TestReadInput(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "in.md")
		if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readInput(p)
		if err != nil {
			t.Fatalf("readInput: %v", err)
		}
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := readInput(filepath.Join(t.TempDir(), "absent.md")); err == nil {
			t.Error("readInput on a missing file returned nil error")
		}
	})

	t.Run("dash reads stdin", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_, _ = w.WriteString("from stdin")
			_ = w.Close()
		}()
		orig := os.Stdin
		os.Stdin = r
		got, err := readInput("-")
		os.Stdin = orig
		_ = r.Close()
		if err != nil {
			t.Fatalf("readInput(-): %v", err)
		}
		if string(got) != "from stdin" {
			t.Errorf("got %q, want %q", got, "from stdin")
		}
	})
}

// buildEngine must not dial at construction time: it is called before
// any decision to rewrite, so a dial here would add daemon latency to
// writes that end up bailing out anyway.
func TestBuildEngineDoesNotDial(t *testing.T) {
	engine, err := buildEngine()
	if err != nil {
		t.Fatalf("buildEngine: %v", err)
	}
	if engine == nil || engine.Backend == nil {
		t.Fatal("buildEngine returned an engine with no backend")
	}
}

// buildHookOutput must error rather than return a partial envelope when
// it cannot find tool_input. RunWriteHook maps the error to a silent
// bail; returning output with tool_input dropped or replaced would make
// Claude Code perform a write with no file_path.
func TestBuildHookOutputErrorsOnUnusableEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
	}{
		{"not json", `{"tool_input":`},
		{"tool_input missing", `{"tool_name":"Write"}`},
		{"tool_input is not an object", `{"tool_input":"a string"}`},
		{"tool_input is null", `{"tool_input":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := buildHookOutput([]byte(tc.env), "content", "REWRITTEN")
			if err == nil {
				t.Errorf("no error for %s; got output %q", tc.name, out)
			}
			if out != nil {
				t.Errorf("returned %d bytes alongside an error; the caller would emit them", len(out))
			}
		})
	}
}

// The Edit tool's field must be rewritable too, not just Write's — the
// hook is registered for both, and covering only "content" would leave
// every Edit silently unrewritten (or worse, rewritten in the wrong key).
func TestBuildHookOutputRewritesEditField(t *testing.T) {
	env := []byte(`{"tool_input":{"file_path":"/tmp/a.md","old_string":"KEEP","new_string":"ORIGINAL"}}`)

	out, err := buildHookOutput(env, "new_string", "REWRITTEN")
	if err != nil {
		t.Fatalf("buildHookOutput: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	ui := got["hookSpecificOutput"].(map[string]any)["updatedInput"].(map[string]any)
	if ui["new_string"] != "REWRITTEN" {
		t.Errorf("new_string = %v, want REWRITTEN", ui["new_string"])
	}
	// old_string is the match target: rewriting it would make the Edit
	// fail to apply, or apply to the wrong text.
	if ui["old_string"] != "KEEP" {
		t.Errorf("old_string was modified: %v", ui["old_string"])
	}
}

// The CLI's usage errors are exit 2, distinct from ExitReadFailed(3), so
// a caller can tell "you invoked me wrong" from "the file isn't there".
func TestShorthandUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"unknown flag", []string{"--nope", "x.md"}},
		{"no path", []string{"--quiet"}},
		{"two paths", []string{"--quiet", "a.md", "b.md"}},
		// --validate needs a file to compare against; stdin bytes are
		// not a file, so this is a usage error rather than a silent
		// validate-of-nothing.
		{"--validate with stdin", []string{"--validate", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := Run(tc.argv); code != ExitUsage {
				t.Errorf("Run(%q) = %d, want ExitUsage(%d)", tc.argv, code, ExitUsage)
			}
		})
	}
}

// A missing input file is ExitReadFailed(3) — not a panic, and not a
// silent success that would leave the caller thinking it compressed.
func TestShorthandMissingFileExits3(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.md")
	if code := Run([]string{"--quiet", absent}); code != ExitReadFailed {
		t.Errorf("exit = %d, want ExitReadFailed(%d)", code, ExitReadFailed)
	}
}

// Empty input is exit 0 with nothing done: there is nothing to compress,
// and treating it as an error would make the hook noisy on new files.
func TestShorthandEmptyInputExitsOK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"--quiet", p}); code != ExitOK {
		t.Errorf("exit = %d, want ExitOK(%d)", code, ExitOK)
	}
}
