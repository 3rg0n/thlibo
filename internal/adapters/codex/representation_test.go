package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write is a test helper: create path with content, failing the test on
// error.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// layer returns (configPath, hooksJSONPath) inside a fresh temp dir,
// standing in for a Codex config layer like ~/.codex.
func layer(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "config.toml"),
		filepath.Join(dir, "hooks.json"),
		filepath.Join(dir, hookMarker)
}

// TestDetectRepresentationEmptyLayer: nothing installed anywhere → inline,
// thlibo's default (and git-ai's).
func TestDetectRepresentationEmptyLayer(t *testing.T) {
	cfg, hj, _ := layer(t)
	if got := DetectRepresentation(cfg, hj); got != RepInline {
		t.Errorf("empty layer: got %v, want inline", got)
	}
}

// TestDetectRepresentationInlineLayer: another tool already writes inline
// (git-ai/taco do) → inline.
func TestDetectRepresentationInlineLayer(t *testing.T) {
	cfg, hj, _ := layer(t)
	write(t, cfg, `model = "gpt-5"

[[hooks.PostToolUse]]

[[hooks.PostToolUse.hooks]]
command = 'C:\tools\git-ai.exe checkpoint codex'
type = "command"
`)
	if got := DetectRepresentation(cfg, hj); got != RepInline {
		t.Errorf("inline layer: got %v, want inline", got)
	}
}

// TestDetectRepresentationHooksJSONLayer is the #170 mirror case: the
// layer's hooks live in hooks.json and config.toml has none, so writing
// inline would create the dual-representation state Codex warns about.
func TestDetectRepresentationHooksJSONLayer(t *testing.T) {
	cfg, hj, _ := layer(t)
	write(t, cfg, "model = \"gpt-5\"\n\n[features]\nhooks = true\n")
	write(t, hj, `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"C:/tools/othertool.exe hook"}]}]}}`)
	if got := DetectRepresentation(cfg, hj); got != RepHooksJSON {
		t.Errorf("hooks.json layer: got %v, want hooks.json", got)
	}
}

// TestDetectRepresentationIgnoresHooksState is the trap this detection
// exists to avoid. Codex records per-hook trust in [hooks.state] keyed by
// the file that defined the hook — so a layer whose hooks live ENTIRELY in
// hooks.json still grows a [hooks.state.'…/hooks.json:…'] table in
// config.toml once the user trusts one. Counting that as an inline hook
// would report "inline" for exactly the hooks.json-only layer we're trying
// to detect, and the mirror bug would survive the fix.
func TestDetectRepresentationIgnoresHooksState(t *testing.T) {
	cfg, hj, _ := layer(t)
	write(t, cfg, `model = "gpt-5"

[features]
hooks = true

[hooks.state]

[hooks.state.'C:\Users\me\.codex\hooks.json:post_tool_use:0:0']
enabled = true
trusted_hash = "sha256:deadbeef"
`)
	write(t, hj, `{"hooks":{"PostToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"C:/tools/othertool.exe hook"}]}]}}`)
	if got := DetectRepresentation(cfg, hj); got != RepHooksJSON {
		t.Errorf("hooks.state must not count as an inline hook: got %v, want hooks.json", got)
	}
	if configHasInlineHooks("[hooks.state]\n") {
		t.Error("configHasInlineHooks counted [hooks.state] as an inline hook")
	}
	if configHasInlineHooks("[hooks.state.'a:b:0:0']\n") {
		t.Error("configHasInlineHooks counted a [hooks.state.*] subtable as an inline hook")
	}
}

// TestDetectRepresentationOnlyThliboInHooksJSON: a stale pre-#170 thlibo
// entry is not evidence the layer prefers hooks.json — it's the thing
// RemoveStaleHooksJSON cleans up. Treating it as evidence would pin thlibo
// to hooks.json forever and defeat the #170 migration.
func TestDetectRepresentationOnlyThliboInHooksJSON(t *testing.T) {
	cfg, hj, hook := layer(t)
	write(t, cfg, "model = \"gpt-5\"\n")
	write(t, hj, `{"hooks":{"PostToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"`+normalisePath(hook)+`"}]}]}}`)
	if got := DetectRepresentation(cfg, hj); got != RepInline {
		t.Errorf("stale thlibo-only hooks.json: got %v, want inline (migrate)", got)
	}
}

// TestDetectRepresentationMalformedHooksJSON: unreadable evidence is not
// evidence. Fall back to inline, which never edits a file thlibo can't
// parse.
func TestDetectRepresentationMalformedHooksJSON(t *testing.T) {
	cfg, hj, _ := layer(t)
	write(t, hj, "{ this is not json")
	if got := DetectRepresentation(cfg, hj); got != RepInline {
		t.Errorf("malformed hooks.json: got %v, want inline", got)
	}
}

// TestDetectRepresentationEmptyHooksJSON: a hooks.json with no actual
// entries (or an empty file) doesn't claim the layer.
func TestDetectRepresentationEmptyHooksJSON(t *testing.T) {
	for name, body := range map[string]string{
		"empty file":   "",
		"empty object": "{}",
		"empty hooks":  `{"hooks":{}}`,
		"empty array":  `{"hooks":{"PostToolUse":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, hj, _ := layer(t)
			write(t, hj, body)
			if got := DetectRepresentation(cfg, hj); got != RepInline {
				t.Errorf("%s: got %v, want inline", name, got)
			}
		})
	}
}

// TestDetectRepresentationFlatEntry: some tools write a flat entry (no
// nested hooks[]) — Cursor's shape. Still a declared hook, so it claims
// the layer.
func TestDetectRepresentationFlatEntry(t *testing.T) {
	cfg, hj, _ := layer(t)
	write(t, hj, `{"hooks":{"PostToolUse":[{"command":"C:/tools/other.exe"}]}}`)
	if got := DetectRepresentation(cfg, hj); got != RepHooksJSON {
		t.Errorf("flat hooks.json entry: got %v, want hooks.json", got)
	}
}

// TestInstallHookMirrorCaseLeavesOneRepresentation is the regression test
// for the bug: on a hooks.json-based layer, install must NOT leave hooks
// in both files.
func TestInstallHookMirrorCaseLeavesOneRepresentation(t *testing.T) {
	cfg, hj, hook := layer(t)
	write(t, cfg, "model = \"gpt-5\"\n\n[features]\nhooks = true\n")
	write(t, hj, `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"C:/tools/othertool.exe hook"}]}]}}`)

	rep, err := InstallHook(cfg, hj, hook)
	if err != nil {
		t.Fatalf("InstallHook: %v", err)
	}
	if rep != RepHooksJSON {
		t.Fatalf("rep = %v, want hooks.json", rep)
	}

	cfgBody, _ := os.ReadFile(cfg)
	if configHasInlineHooks(string(cfgBody)) {
		t.Errorf("config.toml gained inline hooks — dual representation:\n%s", cfgBody)
	}

	hjBody, _ := os.ReadFile(hj)
	if !strings.Contains(normalisePath(string(hjBody)), hookMarker) {
		t.Errorf("hooks.json missing thlibo hook:\n%s", hjBody)
	}
	// The other tool's PreToolUse hook survives.
	if !strings.Contains(string(hjBody), "othertool.exe") {
		t.Errorf("hooks.json lost the other tool's hook:\n%s", hjBody)
	}
}

// TestInstallHookInlineCaseUnchanged: the common path still writes inline,
// exactly as before.
func TestInstallHookInlineCaseUnchanged(t *testing.T) {
	cfg, hj, hook := layer(t)
	write(t, cfg, "model = \"gpt-5\"\n")

	rep, err := InstallHook(cfg, hj, hook)
	if err != nil {
		t.Fatalf("InstallHook: %v", err)
	}
	if rep != RepInline {
		t.Fatalf("rep = %v, want inline", rep)
	}
	cfgBody, _ := os.ReadFile(cfg)
	if !strings.Contains(normalisePath(string(cfgBody)), hookMarker) {
		t.Errorf("config.toml missing inline thlibo hook:\n%s", cfgBody)
	}
	if _, err := os.Stat(hj); !os.IsNotExist(err) {
		t.Error("inline path must not create hooks.json")
	}
}

// TestMergeHooksJSONHookIdempotent: reinstalling must update thlibo's
// entry in place, not accumulate duplicates.
func TestMergeHooksJSONHookIdempotent(t *testing.T) {
	_, hj, hook := layer(t)
	write(t, hj, `{"hooks":{"PostToolUse":[{"matcher":"^Bash$","hooks":[
	  {"type":"command","command":"C:/tools/other.exe"}]}]}}`)

	for i := 0; i < 3; i++ {
		if err := MergeHooksJSONHook(hj, hook); err != nil {
			t.Fatalf("MergeHooksJSONHook #%d: %v", i, err)
		}
	}

	buf, _ := os.ReadFile(hj)
	if n := strings.Count(normalisePath(string(buf)), hookMarker); n != 1 {
		t.Errorf("thlibo hook appears %d times, want 1:\n%s", n, buf)
	}
	if !strings.Contains(string(buf), "other.exe") {
		t.Errorf("lost the other tool's hook:\n%s", buf)
	}
}

// TestMergeHooksJSONHookPreservesUnrelatedKeys: version and other events
// survive the merge.
func TestMergeHooksJSONHookPreservesUnrelatedKeys(t *testing.T) {
	_, hj, hook := layer(t)
	write(t, hj, `{"version":1,"somethingElse":{"a":1},
	  "hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"x"}]}]}}`)

	if err := MergeHooksJSONHook(hj, hook); err != nil {
		t.Fatalf("MergeHooksJSONHook: %v", err)
	}

	buf, _ := os.ReadFile(hj)
	var root map[string]any
	if err := json.Unmarshal(buf, &root); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, buf)
	}
	if root["version"] != float64(1) {
		t.Errorf("version lost: %v", root["version"])
	}
	if _, ok := root["somethingElse"]; !ok {
		t.Error("unrelated top-level key lost")
	}
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("unrelated event lost")
	}
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("PostToolUse not added")
	}
}

// TestMergeHooksJSONHookRefusesMalformed: never clobber a file we can't
// parse.
func TestMergeHooksJSONHookRefusesMalformed(t *testing.T) {
	_, hj, hook := layer(t)
	const broken = "{ not json"
	write(t, hj, broken)
	if err := MergeHooksJSONHook(hj, hook); err == nil {
		t.Error("want error on malformed hooks.json, got nil")
	}
	buf, _ := os.ReadFile(hj)
	if string(buf) != broken {
		t.Errorf("malformed file was modified: %q", buf)
	}
}

// TestMergeHooksJSONHookWrittenShapeIsRemovable closes the loop: what
// MergeHooksJSONHook writes must be exactly what RemoveStaleHooksJSON
// understands, or a later inline migration would leave the entry behind
// and recreate the dual state.
func TestMergeHooksJSONHookWrittenShapeIsRemovable(t *testing.T) {
	_, hj, hook := layer(t)
	if err := MergeHooksJSONHook(hj, hook); err != nil {
		t.Fatalf("MergeHooksJSONHook: %v", err)
	}
	if err := RemoveStaleHooksJSON(hj); err != nil {
		t.Fatalf("RemoveStaleHooksJSON: %v", err)
	}
	if _, err := os.Stat(hj); !os.IsNotExist(err) {
		buf, _ := os.ReadFile(hj)
		t.Errorf("hooks.json should be gone (thlibo was its only entry), got:\n%s", buf)
	}
}
