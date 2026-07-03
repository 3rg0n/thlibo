package processors

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenGoldens captures the PYTHON reference output for each fixture
// into testdata/<name>/<fixture>.golden (+ .input). The permanent
// golden tests then assert Go == this captured Python output, so parity
// is locked even after the run.py files are removed. Regenerate with:
//   GEN_GOLDEN=1 go test ./internal/processors/ -run TestGenGoldens
func TestGenGoldens(t *testing.T) {
	if os.Getenv("GEN_GOLDEN") != "1" {
		t.Skip("set GEN_GOLDEN=1 to regenerate goldens from Python")
	}
	py := pythonBin(t)
	if py == "" {
		t.Fatal("python3 required to regenerate goldens")
	}
	sets := []struct {
		name string
		fix  map[string]string
	}{
		{"git-filter", gitFixtures},
		{"npm-filter", npmFixtures},
		{"cargo-filter", cargoFixtures},
		{"ndjson-filter", ndjsonFixtures},
		{"pytest-filter", pytestFixtures},
		{"stacktrace-filter", stacktraceFixtures},
		{"trivy-filter", trivyFixtures},
		{"go-test-filter", goTestFixtures},
		{"lint-filter", lintFixtures},
	}
	for _, s := range sets {
		script := referenceScript(t, s.name)
		dir := filepath.Join("testdata", s.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for fixName, in := range s.fix {
			cmd := exec.Command(py, script)
			cmd.Stdin = strings.NewReader(in)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s/%s: python failed: %v", s.name, fixName, err)
			}
			if err := os.WriteFile(filepath.Join(dir, fixName+".golden"), out, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fixName+".input"), []byte(in), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
