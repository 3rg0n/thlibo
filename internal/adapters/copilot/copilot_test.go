package copilot

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- embedded-script shape guards ---

// TestPreHookShape: the preToolUse bash hook must call `thlibo rewrite`,
// emit modifiedArgs, and — because Copilot preToolUse is FAIL-CLOSED —
// never deny. It must only ever "allow".
func TestPreHookShape(t *testing.T) {
	s := string(PreHookSh())
	for _, must := range []string{
		"#!/usr/bin/env bash",
		"thlibo rewrite",
		"modifiedArgs",
		`"permissionDecision": "allow"`,
		"exec -- ", // it wraps via thlibo exec
		"THLIBO_DISABLED",
	} {
		if !strings.Contains(s, must) {
			t.Errorf("preToolUse hook missing %q", must)
		}
	}
	// Fail-closed safety: the hook must NOT ever emit a deny decision.
	if strings.Contains(s, `"permissionDecision": "deny"`) || strings.Contains(s, `"deny"`) {
		t.Error("preToolUse hook must never deny (Copilot preToolUse is fail-closed; a deny would block the tool)")
	}
}

// TestPostHookShape: the postToolUse bash hook must pipe through
// `thlibo compress`, emit modifiedResult, and carry the double-
// compression guard against already-wrapped commands.
func TestPostHookShape(t *testing.T) {
	s := string(PostHookSh())
	for _, must := range []string{
		"#!/usr/bin/env bash",
		"thlibo compress",
		"modifiedResult",
		"textResultForLlm",
		"exec -- ", // double-compression guard
		"THLIBO_DISABLED",
	} {
		if !strings.Contains(s, must) {
			t.Errorf("postToolUse hook missing %q", must)
		}
	}
	// It must not EMIT Codex's decision:block JSON (the word "block" may
	// appear in comments; guard on the emitted JSON key instead).
	if strings.Contains(s, `"decision":`) {
		t.Error("postToolUse hook must emit modifiedResult, not Codex's decision field")
	}
}

// TestPS1HooksShape: the PowerShell variants exist and use the same
// Copilot fields (so Windows runs them natively, no bash-wrap).
func TestPS1HooksShape(t *testing.T) {
	pre := string(PreHookPS1())
	if !strings.Contains(pre, "thlibo rewrite") || !strings.Contains(pre, "modifiedArgs") {
		t.Error("pre PS1 hook missing thlibo rewrite / modifiedArgs")
	}
	if !strings.Contains(pre, "ConvertTo-Json") {
		t.Error("pre PS1 hook should emit JSON via ConvertTo-Json")
	}
	post := string(PostHookPS1())
	if !strings.Contains(post, "thlibo compress") || !strings.Contains(post, "modifiedResult") {
		t.Error("post PS1 hook missing thlibo compress / modifiedResult")
	}
}

// --- hooks.json writer ---

// TestWriteHooksJSONSchema: the written file matches Copilot's schema —
// version:1, camelCase preToolUse/postToolUse, each entry with type,
// bash, powershell, timeoutSec; preToolUse carries a matcher, postToolUse
// does not.
func TestWriteHooksJSONSchema(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	jsonPath := filepath.Join(dir, "thlibo.json")
	if err := WriteHooksJSON(jsonPath, hookDir); err != nil {
		t.Fatalf("WriteHooksJSON: %v", err)
	}
	buf, _ := os.ReadFile(jsonPath)

	var doc struct {
		Version int `json:"version"`
		Hooks   struct {
			Pre  []map[string]any `json:"preToolUse"`
			Post []map[string]any `json:"postToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatalf("written hooks.json is not valid JSON: %v\n%s", err, buf)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	if len(doc.Hooks.Pre) != 1 || len(doc.Hooks.Post) != 1 {
		t.Fatalf("want 1 pre + 1 post entry, got %d + %d", len(doc.Hooks.Pre), len(doc.Hooks.Post))
	}
	pre := doc.Hooks.Pre[0]
	if pre["type"] != "command" {
		t.Errorf("pre.type = %v, want command", pre["type"])
	}
	for _, k := range []string{"bash", "powershell", "timeoutSec", "matcher"} {
		if _, ok := pre[k]; !ok {
			t.Errorf("pre entry missing %q", k)
		}
	}
	// The paths must be forward-slashed (bash-safe) and point at our scripts.
	if b, _ := pre["bash"].(string); !strings.HasSuffix(b, PreHookShName) || strings.Contains(b, `\`) {
		t.Errorf("pre.bash = %q, want forward-slashed path ending %s", b, PreHookShName)
	}
	post := doc.Hooks.Post[0]
	if _, ok := post["matcher"]; ok {
		t.Errorf("postToolUse must have NO matcher (compress any tool), got %v", post["matcher"])
	}
	if b, _ := post["bash"].(string); !strings.HasSuffix(b, PostHookShName) {
		t.Errorf("post.bash = %q, want path ending %s", b, PostHookShName)
	}
}

// TestWriteHooksJSONIdempotent: writing twice yields byte-identical
// output (thlibo owns the file; same input → same bytes).
func TestWriteHooksJSONIdempotent(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	jsonPath := filepath.Join(dir, "thlibo.json")
	if err := WriteHooksJSON(jsonPath, hookDir); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(jsonPath)
	if err := WriteHooksJSON(jsonPath, hookDir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(jsonPath)
	if string(first) != string(second) {
		t.Error("WriteHooksJSON not idempotent: second write differs")
	}
}

// TestWriteHookScriptsAndRemove: all four scripts land in hookDir, then
// RemoveHooks deletes them plus the json file, and other files survive.
func TestWriteHookScriptsAndRemove(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	jsonPath := filepath.Join(dir, "copilot", "thlibo.json")
	if err := WriteHookScripts(hookDir); err != nil {
		t.Fatalf("WriteHookScripts: %v", err)
	}
	if err := WriteHooksJSON(jsonPath, hookDir); err != nil {
		t.Fatalf("WriteHooksJSON: %v", err)
	}
	for _, n := range []string{PreHookShName, PreHookPS1Name, PostHookShName, PostHookPS1Name} {
		if _, err := os.Stat(filepath.Join(hookDir, n)); err != nil {
			t.Errorf("script %s not written: %v", n, err)
		}
	}
	// A co-resident other-tool hook file must survive removal.
	other := filepath.Join(filepath.Dir(jsonPath), "git-ai.json")
	_ = os.MkdirAll(filepath.Dir(other), 0o750)
	_ = os.WriteFile(other, []byte(`{"hooks":{}}`), 0o600)

	if err := RemoveHooks(jsonPath, hookDir); err != nil {
		t.Fatalf("RemoveHooks: %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Error("thlibo.json should be deleted")
	}
	for _, n := range []string{PreHookShName, PostHookPS1Name} {
		if _, err := os.Stat(filepath.Join(hookDir, n)); !os.IsNotExist(err) {
			t.Errorf("script %s should be deleted", n)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("co-resident other-tool hook file was wrongly removed")
	}
}

// TestRemoveHooksAbsent: removing when nothing is installed is a no-op.
func TestRemoveHooksAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveHooks(filepath.Join(dir, "nope.json"), filepath.Join(dir, "hooks")); err != nil {
		t.Errorf("RemoveHooks on absent files should be nil, got %v", err)
	}
}

// --- live hook execution (POSIX only) ---

// TestPreHookWrapsCommand drives the real hook-pre.sh with a Copilot
// preToolUse envelope + a fake thlibo that rewrites, asserting the
// emitted JSON is {permissionDecision:"allow", modifiedArgs:{command:…}}.
func TestPreHookWrapsCommand(t *testing.T) {
	out, code := runPreHook(t,
		`{"toolName":"bash","toolArgs":{"command":"git status"}}`,
		"/x/thlibo exec -- git status", 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output not valid JSON: %v (%q)", err, out)
	}
	if resp["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v, want allow", resp["permissionDecision"])
	}
	ma, ok := resp["modifiedArgs"].(map[string]any)
	if !ok {
		t.Fatalf("modifiedArgs missing/not object: %v", resp["modifiedArgs"])
	}
	if ma["command"] != "/x/thlibo exec -- git status" {
		t.Errorf("modifiedArgs.command = %v, want the wrapped command", ma["command"])
	}
}

// TestPreHookPassthrough: every case that must emit NOTHING and exit 0.
// Critically includes the fail-closed cases: a rewrite deny (exit 2) or
// internal error (exit 64) must NEVER become a Copilot deny — it's a
// silent passthrough.
func TestPreHookPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		stdin       string
		rewriteOut  string
		rewriteExit int
	}{
		{"no command field", `{"toolName":"view","toolArgs":{"path":"/x"}}`, "", 0},
		{"empty command", `{"toolName":"bash","toolArgs":{"command":""}}`, "", 0},
		{"no wrapper (exit 1)", `{"toolName":"bash","toolArgs":{"command":"echo hi"}}`, "", 1},
		{"deny rule (exit 2) must NOT deny", `{"toolName":"bash","toolArgs":{"command":"rm -rf /"}}`, "", 2},
		{"internal error (exit 64)", `{"toolName":"bash","toolArgs":{"command":"git status"}}`, "", 64},
		{"rewrite equals input", `{"toolName":"bash","toolArgs":{"command":"git status"}}`, "git status", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runPreHook(t, tc.stdin, tc.rewriteOut, tc.rewriteExit)
			if code != 0 {
				t.Errorf("exit = %d, want 0 (fail-closed host: never non-zero)", code)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("expected no output (passthrough), got %q", out)
			}
		})
	}
}

// TestPreHookKillSwitch: THLIBO_DISABLED short-circuits to passthrough.
func TestPreHookKillSwitch(t *testing.T) {
	out, code := runPreHookEnv(t,
		`{"toolName":"bash","toolArgs":{"command":"git status"}}`,
		"/x/thlibo exec -- git status", 0, []string{"THLIBO_DISABLED=1"})
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("THLIBO_DISABLED should passthrough; got out=%q code=%d", out, code)
	}
}

// TestPostHookCompresses drives hook-post.sh with a large tool result +
// a fake thlibo compress that shrinks it, asserting a modifiedResult.
func TestPostHookCompresses(t *testing.T) {
	big := strings.Repeat("verbose tool output line\n", 200) // > 2000 bytes
	stdin := postEnvelope("cat big.log", big)
	out, code := runPostHook(t, stdin, "SHORT", 0)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output not valid JSON: %v (%q)", err, out)
	}
	mr, ok := resp["modifiedResult"].(map[string]any)
	if !ok {
		t.Fatalf("modifiedResult missing: %v", resp)
	}
	if mr["resultType"] != "success" {
		t.Errorf("resultType = %v, want success", mr["resultType"])
	}
	if mr["textResultForLlm"] != "SHORT" {
		t.Errorf("textResultForLlm = %v, want the compressed text", mr["textResultForLlm"])
	}
}

// TestPostHookGuardsDoubleCompression: a result whose command was already
// wrapped by preToolUse (`… exec -- …`) must pass through untouched, so
// we never re-compress already-filtered output.
func TestPostHookGuardsDoubleCompression(t *testing.T) {
	big := strings.Repeat("verbose tool output line\n", 200)
	stdin := postEnvelope("/x/thlibo exec -- git status", big)
	out, code := runPostHook(t, stdin, "SHORT", 0)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("double-compression guard failed: expected passthrough, got %q", out)
	}
}

// TestPostHookShortCircuitsSmall: output under 2000 bytes passes through
// without spending a compress subprocess.
func TestPostHookShortCircuitsSmall(t *testing.T) {
	stdin := postEnvelope("ls", "a.txt b.txt")
	out, code := runPostHook(t, stdin, "SHORT", 0)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("small output should passthrough, got %q", out)
	}
}

// TestPostHookNoShrinkPassthrough: if compress doesn't actually shrink
// the output, leave the original result alone.
func TestPostHookNoShrinkPassthrough(t *testing.T) {
	big := strings.Repeat("x", 3000)
	stdin := postEnvelope("cat big.log", big)
	// fake compress echoes input back unchanged (>= input length).
	out, code := runPostHookEcho(t, stdin)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("no-shrink result should passthrough, got %q", out)
	}
	_ = big
}

// --- test helpers ---

func postEnvelope(command, output string) string {
	b, _ := json.Marshal(map[string]any{
		"toolName": "bash",
		"toolArgs": map[string]any{"command": command},
		"toolResult": map[string]any{
			"resultType":       "success",
			"textResultForLlm": output,
		},
	})
	return string(b)
}

// runPreHook runs hook-pre.sh with a fake `thlibo rewrite` that prints
// rewriteOut and exits rewriteExit. Returns (stdout, exitcode).
func runPreHook(t *testing.T, stdin, rewriteOut string, rewriteExit int) (string, int) {
	return runPreHookEnv(t, stdin, rewriteOut, rewriteExit, nil)
}

func runPreHookEnv(t *testing.T, stdin, rewriteOut string, rewriteExit int, extraEnv []string) (string, int) {
	t.Helper()
	bash, jqOK := requireBashJQ(t)
	dir := t.TempDir()
	// fake thlibo: `thlibo rewrite <cmd>` prints rewriteOut, exits code.
	writeFakeThlibo(t, dir, "rewrite", rewriteOut, rewriteExit)
	hookPath := filepath.Join(dir, "hook-pre.sh")
	if err := os.WriteFile(hookPath, PreHookSh(), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	_ = jqOK
	return runScript(t, bash, hookPath, stdin, dir, extraEnv)
}

// runPostHook runs hook-post.sh with a fake `thlibo compress` that prints
// compressOut (any subcommand). Returns (stdout, exitcode).
func runPostHook(t *testing.T, stdin, compressOut string, compressExit int) (string, int) {
	t.Helper()
	bash, _ := requireBashJQ(t)
	dir := t.TempDir()
	writeFakeThlibo(t, dir, "compress", compressOut, compressExit)
	hookPath := filepath.Join(dir, "hook-post.sh")
	if err := os.WriteFile(hookPath, PostHookSh(), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	return runScript(t, bash, hookPath, stdin, dir, nil)
}

// runPostHookEcho runs hook-post.sh with a fake thlibo whose compress
// echoes stdin back (no shrink), to prove the no-shrink passthrough.
func runPostHookEcho(t *testing.T, stdin string) (string, int) {
	t.Helper()
	bash, _ := requireBashJQ(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "thlibo")
	// echo stdin back verbatim → compressed len >= input len → passthrough.
	script := "#!/usr/bin/env bash\ncat\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	hookPath := filepath.Join(dir, "hook-post.sh")
	if err := os.WriteFile(hookPath, PostHookSh(), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	return runScript(t, bash, hookPath, stdin, dir, nil)
}

// writeFakeThlibo writes a fake `thlibo` on PATH. For the given wantSub
// subcommand it prints out and exits code; compress reads+discards stdin
// first so a pipe doesn't SIGPIPE.
func writeFakeThlibo(t *testing.T, dir, wantSub, out string, code int) {
	t.Helper()
	// `thlibo compress` gets stdin piped; drain it. `thlibo rewrite` gets
	// the command as $2. Either way: print `out`, exit `code`.
	script := "#!/usr/bin/env bash\n" +
		"if [ \"$1\" = \"compress\" ]; then cat >/dev/null; fi\n" +
		"printf '%s' " + shSingleQuote(out) + "\n" +
		"exit " + itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "thlibo"), []byte(script), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
}

func runScript(t *testing.T, bash, hookPath, stdin, pathDir string, extraEnv []string) (string, int) {
	t.Helper()
	cmd := exec.Command(bash, hookPath)
	cmd.Stdin = strings.NewReader(stdin)
	env := append(os.Environ(), "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("hook run error: %v", err)
	}
	return string(out), code
}

func requireBashJQ(t *testing.T) (string, bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook execution test needs a POSIX shell; skipped on windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}
	return bash, true
}

func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- PowerShell hook execution (runs wherever pwsh exists: CI Windows,
// and any Linux/macOS with PowerShell installed). These close the
// coverage gap the bash-only tests leave: the .ps1 hooks are what
// actually run on Windows, the fail-closed host. ---

// TestPreHookPS1WrapsCommand: the PowerShell preToolUse hook emits
// {permissionDecision:"allow", modifiedArgs:{command:<wrapped>}} and
// preserves other toolArgs keys.
func TestPreHookPS1WrapsCommand(t *testing.T) {
	pwsh := requirePwsh(t)
	dir := t.TempDir()
	writeFakeThliboBat(t, dir, "rewrite", "/x/thlibo exec -- git status", 0)
	out, code := runPS1(t, pwsh, PreHookPS1(), "pre.ps1",
		`{"toolName":"bash","toolArgs":{"command":"git status","cwd":"/x"}}`, dir, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("ps1 output not valid JSON: %v (%q)", err, out)
	}
	if resp["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v, want allow", resp["permissionDecision"])
	}
	ma, _ := resp["modifiedArgs"].(map[string]any)
	if ma["command"] != "/x/thlibo exec -- git status" {
		t.Errorf("modifiedArgs.command = %v, want wrapped", ma["command"])
	}
	if ma["cwd"] != "/x" {
		t.Errorf("modifiedArgs dropped the cwd key: %v", ma)
	}
}

// TestPreHookPS1FailClosedSafe: the CRITICAL invariant on Windows — a
// rewrite deny (exit 2) or internal error (exit 64) must NEVER become a
// Copilot deny. The hook must exit 0 and emit nothing.
func TestPreHookPS1FailClosedSafe(t *testing.T) {
	pwsh := requirePwsh(t)
	for _, code := range []int{1, 2, 64} {
		t.Run("rewrite_exit_"+itoa(code), func(t *testing.T) {
			dir := t.TempDir()
			writeFakeThliboBat(t, dir, "rewrite", "", code)
			out, ec := runPS1(t, pwsh, PreHookPS1(), "pre.ps1",
				`{"toolName":"bash","toolArgs":{"command":"rm -rf /"}}`, dir, nil)
			if ec != 0 {
				t.Errorf("hook exit = %d, want 0 (fail-closed host: never non-zero)", ec)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("want passthrough (no deny), got %q", out)
			}
		})
	}
}

// TestPostHookPS1Compresses: the PowerShell postToolUse hook replaces a
// large result via modifiedResult.
func TestPostHookPS1Compresses(t *testing.T) {
	pwsh := requirePwsh(t)
	dir := t.TempDir()
	writeFakeThliboBat(t, dir, "compress", "SHORT", 0)
	big := strings.Repeat("verbose tool output line\n", 200)
	out, code := runPS1(t, pwsh, PostHookPS1(), "post.ps1", postEnvelope("cat big.log", big), dir, nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("ps1 output not valid JSON: %v (%q)", err, out)
	}
	mr, _ := resp["modifiedResult"].(map[string]any)
	if mr["textResultForLlm"] != "SHORT" {
		t.Errorf("textResultForLlm = %v, want SHORT", mr["textResultForLlm"])
	}
}

// TestPostHookPS1GuardsDoubleCompression: an already-wrapped command
// passes through untouched on Windows too.
func TestPostHookPS1GuardsDoubleCompression(t *testing.T) {
	pwsh := requirePwsh(t)
	dir := t.TempDir()
	writeFakeThliboBat(t, dir, "compress", "SHORT", 0)
	big := strings.Repeat("verbose tool output line\n", 200)
	out, code := runPS1(t, pwsh, PostHookPS1(), "post.ps1",
		postEnvelope("/x/thlibo exec -- git status", big), dir, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("double-compression guard failed on PS1: got %q", out)
	}
}

// TestPreHookPS1KillSwitch: THLIBO_DISABLED passes through on Windows.
func TestPreHookPS1KillSwitch(t *testing.T) {
	pwsh := requirePwsh(t)
	dir := t.TempDir()
	writeFakeThliboBat(t, dir, "rewrite", "/x/thlibo exec -- git status", 0)
	out, code := runPS1(t, pwsh, PreHookPS1(), "pre.ps1",
		`{"toolName":"bash","toolArgs":{"command":"git status"}}`, dir,
		[]string{"THLIBO_DISABLED=1"})
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("THLIBO_DISABLED should passthrough; got out=%q code=%d", out, code)
	}
}

func requirePwsh(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no pwsh/powershell available; PS1 hook test skipped")
	return ""
}

// writeFakeThliboBat writes a fake `thlibo` that PowerShell's `& thlibo`
// resolves. On Windows a .bat/.cmd is executable via PATHEXT; elsewhere
// pwsh resolves a bare shell script with a shebang. We write both a
// no-extension shell script (Unix pwsh) and a .bat (Windows pwsh).
func writeFakeThliboBat(t *testing.T, dir, wantSub, out string, code int) {
	t.Helper()
	// Windows .bat
	bat := "@echo off\r\n" +
		"if \"%1\"==\"compress\" (more>nul & echo " + out + "& exit /b " + itoa(code) + ")\r\n" +
		"echo " + out + "\r\n" +
		"exit /b " + itoa(code) + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, "thlibo.bat"), []byte(bat), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	// Unix shell shim (for pwsh on Linux/macOS).
	sh := "#!/usr/bin/env bash\n" +
		"if [ \"$1\" = \"compress\" ]; then cat >/dev/null; fi\n" +
		"printf '%s' " + shSingleQuote(out) + "\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "thlibo"), []byte(sh), 0o700); err != nil { // #nosec G306
		t.Fatal(err)
	}
	_ = wantSub
}

func runPS1(t *testing.T, pwsh string, script []byte, name, stdin, pathDir string, extraEnv []string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	hookPath := filepath.Join(dir, name)
	if err := os.WriteFile(hookPath, script, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pwsh, "-NoProfile", "-File", hookPath)
	cmd.Stdin = strings.NewReader(stdin)
	env := append(os.Environ(), "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.Output()
	ec := 0
	if ee, ok := err.(*exec.ExitError); ok {
		ec = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("pwsh run error: %v", err)
	}
	return string(out), ec
}

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
