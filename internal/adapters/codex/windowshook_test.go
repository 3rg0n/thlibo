package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forceWindows pins runtimeIsWindows for one test and restores it after,
// so both host paths are covered on every CI leg (#126, mirroring the
// claudecode adapter's helper for #127).
func forceWindows(t *testing.T, win bool) {
	t.Helper()
	prev := runtimeIsWindows
	runtimeIsWindows = func() bool { return win }
	t.Cleanup(func() { runtimeIsWindows = prev })
}

// TestHookFileNamePerHost: Windows gets the PowerShell script, every
// other host the bash one. Registering the .sh on Windows is the #126
// defect — a bare .sh path there resolves through the file association
// (git-bash.exe on a Git-for-Windows box), so the hook never runs.
func TestHookFileNamePerHost(t *testing.T) {
	forceWindows(t, true)
	if got := HookFileName(); got != "thlibo-rewrite-codex.ps1" {
		t.Errorf("on Windows: got %q, want the .ps1", got)
	}
	forceWindows(t, false)
	if got := HookFileName(); got != "thlibo-rewrite-codex.sh" {
		t.Errorf("off Windows: got %q, want the .sh", got)
	}
}

// TestEmbeddedPS1HookShape guards the PowerShell script the same way
// TestEmbeddedHookShape guards the bash one: it must speak Codex's
// decision:block wire and must not need bash or jq.
func TestEmbeddedPS1HookShape(t *testing.T) {
	s := string(HookScriptPS1())
	for _, want := range []string{
		"thlibo compress",
		"decision",
		"'block'",
		"PostToolUse",
		"tool_response",
		"THLIBO_DISABLED",
		"ConvertFrom-Json",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ps1 hook missing %q", want)
		}
	}
	for _, forbidden := range []string{"#!/usr/bin/env bash", "jq ", "permissionDecision"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("ps1 hook contains %q — it must not need bash/jq, and must use PostToolUse/decision", forbidden)
		}
	}
	// Fail open: every path that can't compress leaves the tool result
	// alone by exiting 0 with no stdout (invariant #2).
	if !strings.Contains(s, "exit 0") {
		t.Error("ps1 hook has no fail-open exit")
	}
	// PowerShell 5.1 defaults $OutputEncoding to ASCII, so without these
	// the pipe into `thlibo compress` turns every non-ASCII character into
	// "?" and the model reads mangled output. Measured on this repo's own
	// `go test -v`: `2048 windows × 256 dims` arrived as `2048 windows ?
	// 256 dims`.
	// [Console]::OutputEncoding is the third leg and not cosmetic: it also
	// decodes a child process's stdout, so without it `thlibo compress`'s
	// UTF-8 answer comes back through the OEM code page (#134).
	for _, want := range []string{
		"$OutputEncoding = [System.Text.UTF8Encoding]::new($false)",
		"try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }",
		"OpenStandardInput()",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ps1 hook missing UTF-8 handling %q", want)
		}
	}
	if strings.Contains(s, "[Console]::In.ReadToEnd()") {
		t.Error("ps1 hook reads stdin via [Console]::In, which decodes with the OEM code page")
	}
}

// TestWriteHookScriptPicksBodyByExtension: the .ps1 filename gets the
// PowerShell body, not the bash one. Getting this wrong would write a
// bash script under a .ps1 name — installed, trusted, and inert.
func TestWriteHookScriptPicksBodyByExtension(t *testing.T) {
	dir := t.TempDir()
	ps1 := filepath.Join(dir, "thlibo-rewrite-codex.ps1")
	if err := WriteHookScript(ps1); err != nil {
		t.Fatalf("WriteHookScript(.ps1): %v", err)
	}
	got, _ := os.ReadFile(ps1)
	if string(got) != string(HookScriptPS1()) {
		t.Error(".ps1 path did not receive the PowerShell body")
	}
}

// TestMergeConfigTOMLHookPS1IsWrapped: a .ps1 hook is invoked through
// powershell -File, never as a bare path. A bare .ps1 in a command
// string is not executable — the shell that runs it reports a syntax
// error, and fail-open swallows that.
func TestMergeConfigTOMLHookPS1IsWrapped(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	hook := filepath.Join(dir, "thlibo-rewrite-codex.ps1")
	if err := MergeConfigTOMLHook(cfg, hook); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	s := string(mustRead(t, cfg))
	want := `command = 'powershell -NoProfile -ExecutionPolicy Bypass -File "` +
		normalisePath(hook) + `"'`
	if !strings.Contains(s, want) {
		t.Errorf("missing wrapped command %q:\n%s", want, s)
	}
}

// TestMergeConfigTOMLHookReplacesStaleShEntry is the #126 fix proper: an
// upgrade on Windows finds the old .sh entry and must REWRITE it, not
// append the .ps1 beside it. Markers identify a hook by file, so a second
// block would leave the broken hook declared and firing too (#128).
func TestMergeConfigTOMLHookReplacesStaleShEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	sh := normalisePath(filepath.Join(dir, "thlibo-rewrite-codex.sh"))
	ps1 := filepath.Join(dir, "thlibo-rewrite-codex.ps1")

	existing := "model = \"gpt-5.5\"\n\n[features]\nhooks = true\n\n" +
		"[[hooks.PostToolUse]]\nmatcher = \"^Bash$\"\n\n" +
		"[[hooks.PostToolUse.hooks]]\ntype = \"command\"\ncommand = '" + sh + "'\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MergeConfigTOMLHook(cfg, ps1); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	s := string(mustRead(t, cfg))

	if strings.Contains(s, hookMarkerSh) {
		t.Errorf("stale .sh entry still declared:\n%s", s)
	}
	if !strings.Contains(s, hookMarkerPS1) {
		t.Errorf(".ps1 entry not written:\n%s", s)
	}
	if n := strings.Count(s, "[[hooks.PostToolUse]]"); n != 1 {
		t.Errorf("expected 1 PostToolUse block, got %d:\n%s", n, s)
	}
	// Everything around the rewritten line survives.
	for _, want := range []string{`model = "gpt-5.5"`, "[features]", "hooks = true", `matcher = "^Bash$"`} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q from the config:\n%s", want, s)
		}
	}
}

// TestMergeConfigTOMLHookPS1Idempotent: repeat installs converge, both on
// the literal string thlibo writes and on the basic string Codex writes
// when it re-serialises the file.
func TestMergeConfigTOMLHookPS1Idempotent(t *testing.T) {
	dir := t.TempDir()
	ps1 := filepath.Join(dir, "thlibo-rewrite-codex.ps1")

	t.Run("own literal form", func(t *testing.T) {
		cfg := filepath.Join(dir, "own.toml")
		for i := 0; i < 4; i++ {
			if err := MergeConfigTOMLHook(cfg, ps1); err != nil {
				t.Fatalf("pass %d: %v", i, err)
			}
		}
		s := string(mustRead(t, cfg))
		if n := strings.Count(s, hookMarkerPS1); n != 1 {
			t.Errorf("hook appears %d times after 4 installs, want 1:\n%s", n, s)
		}
	})

	t.Run("codex re-serialised form", func(t *testing.T) {
		cfg := filepath.Join(dir, "reserialised.toml")
		body := "[[hooks.PostToolUse]]\nmatcher = \"^Bash$\"\n\n" +
			"[[hooks.PostToolUse.hooks]]\ntype = \"command\"\n" +
			`command = "powershell -NoProfile -ExecutionPolicy Bypass -File \"` +
			normalisePath(ps1) + `\""` + "\n"
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := MergeConfigTOMLHook(cfg, ps1); err != nil {
			t.Fatalf("MergeConfigTOMLHook: %v", err)
		}
		if got := string(mustRead(t, cfg)); got != body {
			t.Errorf("re-serialised form was rewritten:\ngot:\n%s\nwant:\n%s", got, body)
		}
	})
}

// TestMergeConfigTOMLHookKeepsCRLF: a Windows config.toml is often CRLF,
// and the rewrite splits on "\n". Dropping the trailing "\r" would leave a
// line holding nothing but a carriage return, which a strict TOML parser
// rejects — thlibo would corrupt the config it was fixing.
func TestMergeConfigTOMLHookKeepsCRLF(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	sh := normalisePath(filepath.Join(dir, "thlibo-rewrite-codex.sh"))
	ps1 := filepath.Join(dir, "thlibo-rewrite-codex.ps1")

	existing := strings.ReplaceAll("[[hooks.PostToolUse]]\nmatcher = \"^Bash$\"\n\n"+
		"[[hooks.PostToolUse.hooks]]\ntype = \"command\"\ncommand = '"+sh+"'\n"+
		"\n[features]\nhooks = false\n", "\n", "\r\n")
	if err := os.WriteFile(cfg, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MergeConfigTOMLHook(cfg, ps1); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	if err := EnableHooksFeatureFlag(cfg); err != nil {
		t.Fatalf("EnableHooksFeatureFlag: %v", err)
	}

	s := string(mustRead(t, cfg))
	if !strings.Contains(s, hookMarkerPS1) {
		t.Errorf(".ps1 entry not written:\n%q", s)
	}
	if !strings.Contains(s, "hooks = true") {
		t.Errorf("feature flag not flipped:\n%q", s)
	}
	// No line may consist of a bare carriage return: every "\r" must still
	// be followed by its "\n".
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(strings.TrimSuffix(line, "\r"), "\r") {
			t.Errorf("line %d holds a stray carriage return: %q", i+1, line)
		}
	}
}

// TestMergeConfigTOMLHookIgnoresTrustState: Codex's [hooks.state] table is
// keyed by the defining file, so it keeps naming our script after the hook
// itself is gone. A whole-file substring match would read that inert record
// as an installed hook and make install a silent no-op.
func TestMergeConfigTOMLHookIgnoresTrustState(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	sh := filepath.Join(dir, "thlibo-rewrite-codex.sh")

	existing := "[hooks.state.'" + normalisePath(cfg) + ":post_tool_use:0:0']\n" +
		"enabled = true\ntrusted_hash = \"sha256:dead\"\n" +
		"# stale record naming " + hookMarkerSh + "\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MergeConfigTOMLHook(cfg, sh); err != nil {
		t.Fatalf("MergeConfigTOMLHook: %v", err)
	}
	s := string(mustRead(t, cfg))
	if !strings.Contains(s, "[[hooks.PostToolUse]]") {
		t.Errorf("install was a no-op — the trust record shadowed the append:\n%s", s)
	}
}

// TestMergeHooksJSONHookReplacesStaleShEntry: the hooks.json path carries
// the same swap, in place, so a Windows upgrade doesn't leave two entries.
func TestMergeHooksJSONHookReplacesStaleShEntry(t *testing.T) {
	dir := t.TempDir()
	hj := filepath.Join(dir, "hooks.json")
	sh := normalisePath(filepath.Join(dir, "thlibo-rewrite-codex.sh"))
	ps1 := filepath.Join(dir, "thlibo-rewrite-codex.ps1")

	body := `{"hooks":{"PostToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"C:/tools/other.exe"},
	  {"type":"command","command":"` + sh + `"}]}]}}`
	if err := os.WriteFile(hj, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MergeHooksJSONHook(hj, ps1); err != nil {
		t.Fatalf("MergeHooksJSONHook: %v", err)
	}
	raw := mustRead(t, hj)
	s := string(raw)
	if strings.Contains(s, hookMarkerSh) {
		t.Errorf("stale .sh entry still present:\n%s", s)
	}
	if !strings.Contains(s, hookMarkerPS1) || !strings.Contains(s, "powershell -NoProfile") {
		t.Errorf("wrapped .ps1 command not written:\n%s", s)
	}
	if !strings.Contains(s, "other.exe") {
		t.Errorf("lost the other tool's hook:\n%s", s)
	}
	// Still one thlibo entry, and the file still parses.
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("hooks.json no longer parses: %v", err)
	}
	if n := strings.Count(s, "thlibo-rewrite-codex"); n != 1 {
		t.Errorf("thlibo appears %d times, want 1:\n%s", n, s)
	}
}

// TestRemoveStaleHooksJSONDropsPS1Entry: cleanup recognises both scripts.
// Matching only the .sh would leave a pre-existing .ps1 entry behind and
// recreate the dual-representation state #170 fixed.
func TestRemoveStaleHooksJSONDropsPS1Entry(t *testing.T) {
	dir := t.TempDir()
	hj := filepath.Join(dir, "hooks.json")
	body := `{"version":1,"hooks":{"PostToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"powershell -NoProfile -ExecutionPolicy Bypass -File \"C:/h/thlibo-rewrite-codex.ps1\""}]}]}}`
	if err := os.WriteFile(hj, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStaleHooksJSON(hj); err != nil {
		t.Fatalf("RemoveStaleHooksJSON: %v", err)
	}
	s := string(mustRead(t, hj))
	if strings.Contains(s, hookMarkerPS1) {
		t.Errorf(".ps1 entry not removed:\n%s", s)
	}
	if !strings.Contains(s, `"version": 1`) {
		t.Errorf("unrelated keys lost:\n%s", s)
	}
}
