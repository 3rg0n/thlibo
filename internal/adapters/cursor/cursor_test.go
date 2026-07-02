package cursor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestEmbeddedHookShape guards the bash script against regressions.
// The Cursor hook uses preToolUse + updated_input to REWRITE the Shell
// command (so its output is compressed when run) — Cursor can't
// substitute shell output the way Codex's PostToolUse decision:block
// does, so this is the command-wrap path (same as Claude Code's Bash
// hook), scoped to tool_name == "Shell".
func TestEmbeddedHookShape(t *testing.T) {
	s := string(HookScript())
	must := []string{
		"#!/usr/bin/env bash",
		"thlibo rewrite",
		"updated_input",
		`"permission"`,
		`.tool_name`,
		"Shell",
	}
	for _, m := range must {
		if !strings.Contains(s, m) {
			t.Errorf("Cursor hook missing %q", m)
		}
	}
	// Cursor shell hooks cannot substitute output — guard against a
	// draft that tried Codex's decision:block or Claude's
	// permissionDecision, neither of which Cursor understands. Check the
	// emitted-JSON forms (the jq object keys), not bare words, so the
	// explanatory comments in the script don't trip the guard.
	for _, forbidden := range []string{`"decision":`, `permissionDecision`, `"updated_mcp_tool_output":`} {
		for _, line := range strings.Split(s, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue // explanatory comment, not emitted JSON
			}
			if strings.Contains(trimmed, forbidden) {
				t.Errorf("Cursor hook uses forbidden mechanism %q in a non-comment line: %s", forbidden, trimmed)
			}
		}
	}
}

func TestWriteHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-rewrite-cursor.sh")
	if err := WriteHookScript(dest); err != nil {
		t.Fatalf("WriteHookScript: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !reflect.DeepEqual(got, HookScript()) {
		t.Error("written bytes differ from embedded script")
	}
}

// shellHookIn/readHookIn are the two hook paths a real install passes.
func hookPaths(dir string) (string, string) {
	return filepath.Join(dir, "thlibo-rewrite-cursor.sh"), filepath.Join(dir, "thlibo-read-cursor.sh")
}

// TestMergeHooksJSONFreshFile: the written file matches the Cursor
// schema (top-level version + hooks.preToolUse array of flat
// {matcher, command} objects), with BOTH a Shell and a Read entry.
func TestMergeHooksJSONFreshFile(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	shellHook, readHook := hookPaths(dir)
	if err := MergeHooksJSON(hp, shellHook, readHook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	if err := json.Unmarshal(buf, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if root["version"] != float64(1) {
		t.Errorf("expected version 1, got %v", root["version"])
	}
	pre := root["hooks"].(map[string]any)["preToolUse"].([]any)
	byMatcher := map[string]string{}
	for _, e := range pre {
		obj := e.(map[string]any)
		byMatcher[obj["matcher"].(string)] = obj["command"].(string)
	}
	if !strings.Contains(byMatcher["Shell"], "thlibo-rewrite-cursor.sh") {
		t.Errorf("Shell entry wrong/missing: %v", byMatcher["Shell"])
	}
	if !strings.Contains(byMatcher["Read"], "thlibo-read-cursor.sh") {
		t.Errorf("Read entry wrong/missing: %v", byMatcher["Read"])
	}
}

// TestMergeHooksJSONPreservesOtherEventsAndKeys: an existing hooks.json
// with other events and a custom version is preserved; we add our
// Shell + Read entries alongside an unrelated preToolUse hook.
func TestMergeHooksJSONPreservesOtherEventsAndKeys(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	existing := `{
  "version": 1,
  "hooks": {
    "afterFileEdit": [{ "command": "./fmt.sh" }],
    "preToolUse": [{ "matcher": "Write", "command": "./audit.sh" }]
  }
}`
	_ = os.WriteFile(hp, []byte(existing), 0o600)

	shellHook, readHook := hookPaths(dir)
	if err := MergeHooksJSON(hp, shellHook, readHook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	_ = json.Unmarshal(buf, &root)

	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["afterFileEdit"]; !ok {
		t.Error("afterFileEdit event lost")
	}
	pre := hooks["preToolUse"].([]any)
	if len(pre) != 3 {
		t.Fatalf("expected 3 preToolUse entries (audit + shell + read), got %d", len(pre))
	}
	var foundAudit, foundShell, foundRead bool
	for _, e := range pre {
		cmd, _ := e.(map[string]any)["command"].(string)
		switch {
		case strings.Contains(cmd, "audit.sh"):
			foundAudit = true
		case strings.Contains(cmd, "thlibo-rewrite-cursor.sh"):
			foundShell = true
		case strings.Contains(cmd, "thlibo-read-cursor.sh"):
			foundRead = true
		}
	}
	if !foundAudit {
		t.Error("unrelated Write audit hook was dropped")
	}
	if !foundShell || !foundRead {
		t.Errorf("thlibo hooks not both added (shell=%v read=%v)", foundShell, foundRead)
	}
}

// TestMergeHooksJSONIdempotent: three merges produce exactly one Shell
// and one Read thlibo entry (each recognised by its marker).
func TestMergeHooksJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	shellHook, readHook := hookPaths(dir)
	for i := 0; i < 3; i++ {
		if err := MergeHooksJSON(hp, shellHook, readHook); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	buf, _ := os.ReadFile(hp)
	if n := strings.Count(string(buf), "thlibo-rewrite-cursor.sh"); n != 1 {
		t.Errorf("expected exactly 1 Shell entry, got %d: %s", n, buf)
	}
	if n := strings.Count(string(buf), "thlibo-read-cursor.sh"); n != 1 {
		t.Errorf("expected exactly 1 Read entry, got %d: %s", n, buf)
	}
	// And exactly two thlibo preToolUse entries total.
	var root map[string]any
	_ = json.Unmarshal(buf, &root)
	if got := len(root["hooks"].(map[string]any)["preToolUse"].([]any)); got != 2 {
		t.Errorf("expected 2 preToolUse entries after 3 merges, got %d", got)
	}
}

// TestMergeHooksJSONUpgradeFromShellOnly: the rc.1 -> rc.2 upgrade path.
// A hooks.json that already has ONLY the Shell entry (what rc.1
// installed) must gain the Read entry on re-merge WITHOUT duplicating
// Shell — the exact scenario a user already on the Cursor-Shell RC hits.
func TestMergeHooksJSONUpgradeFromShellOnly(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	shellHook, readHook := hookPaths(dir)
	// Simulate an rc.1 install: only the Shell entry present.
	rc1 := `{
  "version": 1,
  "hooks": {
    "preToolUse": [
      { "matcher": "Shell", "command": "` + normalisePath(shellHook) + `" }
    ]
  }
}`
	_ = os.WriteFile(hp, []byte(rc1), 0o600)

	// rc.2 install re-runs the merge with both hooks.
	if err := MergeHooksJSON(hp, shellHook, readHook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	buf, _ := os.ReadFile(hp)
	if n := strings.Count(string(buf), "thlibo-rewrite-cursor.sh"); n != 1 {
		t.Errorf("expected exactly 1 Shell entry after upgrade, got %d: %s", n, buf)
	}
	if n := strings.Count(string(buf), "thlibo-read-cursor.sh"); n != 1 {
		t.Errorf("expected the Read entry added on upgrade, got %d: %s", n, buf)
	}
	var root map[string]any
	_ = json.Unmarshal(buf, &root)
	pre := root["hooks"].(map[string]any)["preToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected 2 preToolUse entries after upgrade, got %d", len(pre))
	}
	// Confirm both matchers are present and correct.
	byMatcher := map[string]string{}
	for _, e := range pre {
		obj := e.(map[string]any)
		byMatcher[obj["matcher"].(string)] = obj["command"].(string)
	}
	if !strings.Contains(byMatcher["Shell"], "thlibo-rewrite-cursor.sh") {
		t.Errorf("Shell entry wrong after upgrade: %v", byMatcher["Shell"])
	}
	if !strings.Contains(byMatcher["Read"], "thlibo-read-cursor.sh") {
		t.Errorf("Read entry missing/wrong after upgrade: %v", byMatcher["Read"])
	}
}

// TestMergeHooksJSONRejectsInvalidJSON: malformed existing file is not
// clobbered — the merge errors instead of destroying user data.
func TestMergeHooksJSONRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(hp, []byte("{ not json"), 0o600)
	shellHook, readHook := hookPaths(dir)
	if err := MergeHooksJSON(hp, shellHook, readHook); err == nil {
		t.Error("expected an error on malformed JSON, got nil")
	}
	// File must be left as-is.
	buf, _ := os.ReadFile(hp)
	if string(buf) != "{ not json" {
		t.Errorf("malformed file was modified: %q", buf)
	}
}

// TestMergeHooksJSONNormalisesBackslashes: Windows hook paths are
// written with forward slashes so bash -c can execute them.
func TestMergeHooksJSONNormalisesBackslashes(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	winShell := `C:\Users\me\.thlibo\hooks\thlibo-rewrite-cursor.sh`
	winRead := `C:\Users\me\.thlibo\hooks\thlibo-read-cursor.sh`
	if err := MergeHooksJSON(hp, winShell, winRead); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	buf, _ := os.ReadFile(hp)
	if strings.Contains(string(buf), `\`) {
		t.Errorf("backslashes not normalised: %s", buf)
	}
	if !strings.Contains(string(buf), "C:/Users/me/.thlibo/hooks/thlibo-rewrite-cursor.sh") {
		t.Errorf("forward-slash Shell path missing: %s", buf)
	}
	if !strings.Contains(string(buf), "C:/Users/me/.thlibo/hooks/thlibo-read-cursor.sh") {
		t.Errorf("forward-slash Read path missing: %s", buf)
	}
}

// TestMergeHooksJSONPreservesExistingVersion: if the user already set a
// version (e.g. a future 2), we don't override it.
func TestMergeHooksJSONPreservesExistingVersion(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(hp, []byte(`{"version": 2, "hooks": {}}`), 0o600)
	shellHook, readHook := hookPaths(dir)
	if err := MergeHooksJSON(hp, shellHook, readHook); err != nil {
		t.Fatalf("MergeHooksJSON: %v", err)
	}
	var root map[string]any
	buf, _ := os.ReadFile(hp)
	_ = json.Unmarshal(buf, &root)
	if root["version"] != float64(2) {
		t.Errorf("existing version overwritten: got %v, want 2", root["version"])
	}
}

// runHook executes the embedded hook.sh with the given stdin, using a
// fake `thlibo` on PATH that echoes a fixed rewrite and exits with
// rewriteExit. Returns (stdout, exitCode). Skips on non-bash platforms
// or when bash/jq aren't available, so it exercises the real script
// wherever a POSIX shell exists (CI linux/macOS) without a real thlibo.
func runHook(t *testing.T, stdin, rewriteOut string, rewriteExit int) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook.sh execution test needs a POSIX shell; skipped on windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}

	dir := t.TempDir()
	// Fake `thlibo`: ignores args, prints rewriteOut, exits rewriteExit.
	fake := filepath.Join(dir, "thlibo")
	script := "#!/usr/bin/env bash\nprintf '%s' " + shellQuote(rewriteOut) + "\nexit " + itoa(rewriteExit) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { // #nosec G306 -- test shim needs exec bit
		t.Fatal(err)
	}
	hookPath := filepath.Join(dir, "hook.sh")
	if err := WriteHookScript(hookPath); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(stdin)
	// Prepend our fake-thlibo dir so the hook finds it first.
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run hook: %v", err)
	}
	return string(out), code
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestHookOutputWrappableShell drives the real hook.sh with a Shell
// preToolUse envelope + a fake thlibo that rewrites the command, and
// asserts the emitted JSON matches Cursor's contract:
// {"permission":"allow","updated_input":{"command":<rewritten>}}.
func TestHookOutputWrappableShell(t *testing.T) {
	stdin := `{"tool_name":"Shell","tool_input":{"command":"git status"},"tool_use_id":"t1"}`
	out, code := runHook(t, stdin, "thlibo exec -- git status", 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output not valid JSON: %v (%q)", err, out)
	}
	if resp["permission"] != "allow" {
		t.Errorf("permission = %v, want allow", resp["permission"])
	}
	ui, ok := resp["updated_input"].(map[string]any)
	if !ok {
		t.Fatalf("updated_input missing/not object: %v", resp["updated_input"])
	}
	if ui["command"] != "thlibo exec -- git status" {
		t.Errorf("updated_input.command = %v, want the wrapped command", ui["command"])
	}
	// Must not use unsupported mechanisms.
	if _, bad := resp["decision"]; bad {
		t.Error("response must not carry Codex's decision field")
	}
}

// TestHookOutputPassthrough covers every case that must emit NOTHING and
// exit 0: non-Shell tool, empty command, non-wrappable command (rewrite
// exit 1), and the reserved exit-3 (no ask path on Cursor).
func TestHookOutputPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		stdin       string
		rewriteOut  string
		rewriteExit int
	}{
		{"non-Shell tool", `{"tool_name":"Read","tool_input":{"path":"/x"}}`, "", 0},
		{"empty command", `{"tool_name":"Shell","tool_input":{"command":""}}`, "", 0},
		{"no wrapper (exit 1)", `{"tool_name":"Shell","tool_input":{"command":"echo hi"}}`, "", 1},
		{"reserved ask (exit 3) has no path", `{"tool_name":"Shell","tool_input":{"command":"git status"}}`, "thlibo exec -- git status", 3},
		{"rewrite equals input", `{"tool_name":"Shell","tool_input":{"command":"git status"}}`, "git status", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runHook(t, tc.stdin, tc.rewriteOut, tc.rewriteExit)
			if code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("expected no output (passthrough), got %q", out)
			}
		})
	}
}

// TestHookDisabledKillSwitch: THLIBO_DISABLED short-circuits to
// passthrough even for a wrappable command.
func TestHookDisabledKillSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX shell")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "thlibo")
	_ = os.WriteFile(fake, []byte("#!/usr/bin/env bash\nprintf 'thlibo exec -- git status'\nexit 0\n"), 0o700) // #nosec G306
	hookPath := filepath.Join(dir, "hook.sh")
	if err := WriteHookScript(hookPath); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Shell","tool_input":{"command":"git status"}}`)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "THLIBO_DISABLED=1")
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("THLIBO_DISABLED should passthrough, got %q", out)
	}
}

// runReadHook executes the embedded hook-read.sh with the given stdin,
// using a fake `thlibo` on PATH whose `case --quiet` prints caseOut and
// exits caseExit, and a fake `timeout` that just execs its target (so
// the timeout guard is transparent in tests). Returns (stdout, exit).
// The compressed.log inside caseOut is pre-created so the hook's
// existence check passes. Skips on non-POSIX / missing bash+jq.
func runReadHook(t *testing.T, stdin, caseOut string, caseExit int) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook-read.sh execution test needs a POSIX shell; skipped on windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	dir := t.TempDir()
	// If the fake case should "succeed", pre-create its compressed.log.
	if caseExit == 0 && caseOut != "" {
		_ = os.MkdirAll(caseOut, 0o755)
		_ = os.WriteFile(filepath.Join(caseOut, "compressed.log"), []byte("compressed\n"), 0o644)
	}
	fake := filepath.Join(dir, "thlibo")
	// `thlibo case --quiet <path>` -> print caseOut, exit caseExit.
	script := "#!/usr/bin/env bash\nprintf '%s' " + shellQuote(caseOut) + "\nexit " + itoa(caseExit) + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	hookPath := filepath.Join(dir, "hook-read.sh")
	if err := WriteReadHookScript(hookPath); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run read hook: %v", err)
	}
	return string(out), code
}

// TestReadHookRewritesLargeLog: a large .log Read is redirected to the
// case's compressed.log via updated_input.file_path.
func TestReadHookRewritesLargeLog(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "app.log")
	_ = os.WriteFile(big, make([]byte, 40*1024), 0o644) // > 32 KiB size gate
	caseDir := filepath.Join(dir, "case")
	stdin := `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(big) + `"}}`
	out, code := runReadHook(t, stdin, caseDir, 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("read hook output not JSON: %v (%q)", err, out)
	}
	if resp["permission"] != "allow" {
		t.Errorf("permission = %v, want allow", resp["permission"])
	}
	ui, _ := resp["updated_input"].(map[string]any)
	fp, _ := ui["file_path"].(string)
	if !strings.HasSuffix(fp, "compressed.log") {
		t.Errorf("file_path not redirected to compressed.log: %v", fp)
	}
}

// TestReadHookPassthrough: cases that must emit nothing (exit 0).
func TestReadHookPassthrough(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "app.log")
	_ = os.WriteFile(big, make([]byte, 40*1024), 0o644)
	small := filepath.Join(dir, "tiny.log")
	_ = os.WriteFile(small, []byte("hi\n"), 0o644)
	src := filepath.Join(dir, "main.go")
	_ = os.WriteFile(src, make([]byte, 40*1024), 0o644)

	cases := []struct {
		name     string
		stdin    string
		caseOut  string
		caseExit int
	}{
		{"non-Read tool", `{"tool_name":"Shell","tool_input":{"command":"ls"}}`, "", 0},
		{"unsupported extension", `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(src) + `"}}`, "", 0},
		{"small file under size gate", `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(small) + `"}}`, "", 0},
		{"low-value case (exit 6)", `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(big) + `"}}`, filepath.Join(dir, "c6"), 6},
		{"timeout (exit 124)", `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(big) + `"}}`, filepath.Join(dir, "c124"), 124},
		{"missing file", `{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(filepath.Join(dir, "nope.log")) + `"}}`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runReadHook(t, tc.stdin, tc.caseOut, tc.caseExit)
			if code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("expected passthrough (no output), got %q", out)
			}
		})
	}
}

// TestReadHookNoTimeoutBinaryPassthrough: when neither `timeout` nor
// `gtimeout` is on PATH (e.g. stock macOS without coreutils), the hook
// must PASSTHROUGH rather than run `thlibo case` unbounded and risk
// hanging Cursor on a slow OCR. Runs with a PATH containing only fakes
// for the required binaries (jq, thlibo) — no timeout.
func TestReadHookNoTimeoutBinaryPassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX shell")
	}
	realBash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	realJq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not available")
	}
	dir := t.TempDir()
	// Use a .pdf: PDFs skip the size gate, so the hook reaches the
	// timeout check without needing `wc` (which isn't on our minimal
	// PATH) — otherwise the test could passthrough at the size gate for
	// the wrong reason.
	big := filepath.Join(dir, "doc.pdf")
	_ = os.WriteFile(big, []byte("%PDF-1.4\n"), 0o644)

	bindir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bindir, 0o755)
	// Symlink the coreutils the hook needs (jq, plus tr/printf/etc. that
	// run before the timeout check) into bindir, but deliberately NOT
	// timeout/gtimeout. bash builtins (command, case, [, printf) work
	// regardless; tr is external so link it.
	for _, bin := range []string{"jq", "tr", "wc", "sed", "cat", "printf"} {
		if p, err := exec.LookPath(bin); err == nil {
			_ = os.Symlink(p, filepath.Join(bindir, bin))
		}
	}
	_ = realJq // kept for the skip-guard above
	// A fake `thlibo case` that, if ever called, would "succeed" — so if
	// the hook DID run it (bug), we'd see output and the test fails.
	caseDir := filepath.Join(dir, "case")
	_ = os.MkdirAll(caseDir, 0o755)
	_ = os.WriteFile(filepath.Join(caseDir, "compressed.log"), []byte("x\n"), 0o644)
	fakeThlibo := "#!/usr/bin/env bash\nprintf '%s' " + shellQuote(caseDir) + "\nexit 0\n"
	_ = os.WriteFile(filepath.Join(bindir, "thlibo"), []byte(fakeThlibo), 0o700) // #nosec G306

	hookPath := filepath.Join(dir, "hook-read.sh")
	if err := WriteReadHookScript(hookPath); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(realBash, hookPath)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"` + normalisePath(big) + `"}}`)
	// PATH = ONLY our fake bindir. bash itself is invoked by absolute
	// path, so it still runs; but `command -v timeout/gtimeout` inside
	// the hook find nothing -> passthrough.
	cmd.Env = []string{"PATH=" + bindir}
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("no timeout binary must passthrough (no output), got %q", out)
	}
}

func TestWriteReadHookScript(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "sub", "thlibo-read-cursor.sh")
	if err := WriteReadHookScript(dest); err != nil {
		t.Fatalf("WriteReadHookScript: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !reflect.DeepEqual(got, ReadHookScript()) {
		t.Error("written bytes differ from embedded read script")
	}
}
