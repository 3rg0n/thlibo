package configcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/thlibo/internal/config"
)

func TestRunInteractiveKeepsCurrentOnEmptyLine(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("THLIBO_CONFIG", cfgPath)

	// Empty answers for every prompt + "y" to confirm.
	in := strings.NewReader("\n\n\n\ny\n")
	var out bytes.Buffer
	code := runInteractive(cfgPath, in, &out, false)
	if code != ExitOK {
		t.Fatalf("exit code %d; output:\n%s", code, out.String())
	}
	// "no changes" path — file should not be written when nothing
	// changed and we hit the early return.
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("expected 'no changes' message; got:\n%s", out.String())
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("file was written despite no-changes; err=%v", err)
	}
}

func TestRunInteractiveTogglesAutoShorthand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("THLIBO_CONFIG", cfgPath)

	// Answer "true" to first question, empty for the rest, "y"
	// to confirm.
	in := strings.NewReader("true\n\n\n\ny\n")
	var out bytes.Buffer
	code := runInteractive(cfgPath, in, &out, false)
	if code != ExitOK {
		t.Fatalf("exit code %d; output:\n%s", code, out.String())
	}

	// Re-load the config — the new file should reflect the change.
	cfg := config.Load()
	if !cfg.AutoShorthandOnWrite {
		t.Errorf("expected auto_shorthand_on_write=true after toggle; got false")
	}
}

func TestRunInteractiveAbortsOnQ(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("THLIBO_CONFIG", cfgPath)

	// First question gets 'q' — abort.
	in := strings.NewReader("q\n")
	var out bytes.Buffer
	code := runInteractive(cfgPath, in, &out, false)
	if code != ExitAborted {
		t.Errorf("expected ExitAborted; got %d", code)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("config file written despite abort")
	}
}

func TestApplySetBoolean(t *testing.T) {
	cfg := config.Defaults()
	if err := applySet(&cfg, "auto_shorthand_on_write", "true"); err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoShorthandOnWrite {
		t.Error("apply did not flip the boolean")
	}
}

func TestApplySetInteger(t *testing.T) {
	cfg := config.Defaults()
	if err := applySet(&cfg, "auto_shorthand_min_bytes", "1024"); err != nil {
		t.Fatal(err)
	}
	if cfg.AutoShorthandMinBytes != 1024 {
		t.Errorf("min_bytes = %d, want 1024", cfg.AutoShorthandMinBytes)
	}
}

func TestApplySetGlobList(t *testing.T) {
	cfg := config.Defaults()
	if err := applySet(&cfg, "auto_shorthand_paths", "**/SKILL.md, **/CLAUDE.md ,foo/bar.yaml"); err != nil {
		t.Fatal(err)
	}
	want := []string{"**/SKILL.md", "**/CLAUDE.md", "foo/bar.yaml"}
	if len(cfg.AutoShorthandPaths) != len(want) {
		t.Fatalf("paths = %v, want %v", cfg.AutoShorthandPaths, want)
	}
	for i, p := range cfg.AutoShorthandPaths {
		if p != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestApplySetUnknownKey(t *testing.T) {
	cfg := config.Defaults()
	err := applySet(&cfg, "totally_made_up_field", "yes")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestApplySetInvalidBoolean(t *testing.T) {
	cfg := config.Defaults()
	err := applySet(&cfg, "auto_shorthand_on_write", "maybe")
	if err == nil {
		t.Error("expected error for non-bool value")
	}
}

func TestWriteAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("THLIBO_CONFIG", cfgPath)

	cfg := config.Defaults()
	cfg.AutoShorthandOnWrite = true
	cfg.AutoShorthandMinBytes = 2048
	cfg.AutoShorthandYAMLProse = true

	if err := writeConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	got := config.Load()
	if !got.AutoShorthandOnWrite {
		t.Error("on_write lost in round-trip")
	}
	if got.AutoShorthandMinBytes != 2048 {
		t.Errorf("min_bytes = %d, want 2048", got.AutoShorthandMinBytes)
	}
	if !got.AutoShorthandYAMLProse {
		t.Error("yaml_prose lost in round-trip")
	}
}

func TestRunShowPrintsCurrentConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	t.Setenv("THLIBO_CONFIG", cfgPath)

	// Pre-write a non-default config so the show output is
	// distinguishable from defaults.
	cfg := config.Defaults()
	cfg.AutoShorthandOnWrite = true
	if err := writeConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	// Capture stdout via os.Pipe so runShow doesn't pollute the
	// test runner's output.
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	code := runShow(cfgPath)
	w.Close()
	os.Stdout = stdout
	if code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	body, _ := readAll(r)
	if !strings.Contains(body, "auto_shorthand_on_write: true") {
		t.Errorf("show output missing toggled field:\n%s", body)
	}
	if !strings.Contains(body, cfgPath) {
		t.Errorf("show output missing source path:\n%s", body)
	}
}

func TestDiffReportsChangedFields(t *testing.T) {
	a := config.Defaults()
	b := a
	b.AutoShorthandOnWrite = true
	b.AutoShorthandMinBytes = 999

	out := diff(a, b)
	if len(out) != 2 {
		t.Fatalf("diff = %v, want 2 lines", out)
	}
	joined := strings.Join(out, " | ")
	if !strings.Contains(joined, "auto_shorthand_on_write") {
		t.Errorf("diff missing on_write change: %v", out)
	}
	if !strings.Contains(joined, "auto_shorthand_min_bytes") {
		t.Errorf("diff missing min_bytes change: %v", out)
	}
}

func readAll(r *os.File) (string, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(r)
	return b.String(), err
}
