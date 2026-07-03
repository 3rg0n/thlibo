package processors

import (
	"testing"
)

var cargoFixtures = map[string]string{
	"build_error": "error[E0382]: borrow of moved value\nFinished dev [unoptimized] target(s) in 0.52s\n",
	"simple_success": "Finished release [optimized] target(s) in 5.23s\n",
	"empty": "",
}

func TestCargoFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available")
	}
	script := referenceScript(t, "cargo-filter")
	for name, in := range cargoFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(cargoFilter([]byte(in)))
			if got != want {
				t.Errorf("parity mismatch: go=%q py=%q", got, want)
			}
		})
	}
}