package processors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenFilters maps each native filter's processor name to its raw
// transform func, for the golden parity test.
var goldenFilters = map[string]func([]byte) []byte{
	"git-filter":        gitFilter,
	"npm-filter":        npmFilter,
	"cargo-filter":      cargoFilter,
	"ndjson-filter":     ndjsonFilter,
	"pytest-filter":     pytestFilter,
	"stacktrace-filter": stacktraceFilter,
	"trivy-filter":      trivyFilter,
	"go-test-filter":    goTestFilter,
	"lint-filter":       lintFilter,
}

// TestFilterGoldens is the permanent parity gate: for every
// testdata/<filter>/<fixture>.input, the Go filter's output must equal
// the captured Python reference in <fixture>.golden (byte-for-byte).
//
// The goldens were captured from the original run.py via TestGenGoldens
// (regenerate with GEN_GOLDEN=1 while the run.py files still exist).
// This runs with NO python3 dependency, so it works in every CI image —
// unlike the earlier live-Python parity tests it replaces (ADR 0010).
func TestFilterGoldens(t *testing.T) {
	root := "testdata"
	for name, fn := range goldenFilters {
		dir := filepath.Join(root, name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("%s: no testdata dir (%v)", name, err)
			continue
		}
		var fixtures int
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".input") {
				continue
			}
			fixName := strings.TrimSuffix(e.Name(), ".input")
			t.Run(name+"/"+fixName, func(t *testing.T) {
				in, err := os.ReadFile(filepath.Join(dir, fixName+".input"))
				if err != nil {
					t.Fatal(err)
				}
				want, err := os.ReadFile(filepath.Join(dir, fixName+".golden"))
				if err != nil {
					t.Fatal(err)
				}
				got := fn(in)
				if string(got) != string(want) {
					t.Errorf("%s/%s: Go output != Python golden\n got: %q\nwant: %q", name, fixName, got, want)
				}
			})
			fixtures++
		}
		if fixtures == 0 {
			t.Errorf("%s: no .input fixtures found", name)
		}
	}
}
