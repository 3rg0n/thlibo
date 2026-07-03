package processors

import (
	"testing"
)

var npmFixtures = map[string]string{
	"install_success": "added 125 packages\n",
	"list_tree": "project@1.0.0\n",
	"empty": "",
}

func TestNpmFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available")
	}
	script := referenceScript(t, "npm-filter")
	for name, in := range npmFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(npmFilter([]byte(in)))
			if got != want {
				t.Errorf("parity mismatch: go=%q py=%q", got, want)
			}
		})
	}
}