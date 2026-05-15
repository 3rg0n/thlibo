package middleware

import (
	"testing"

	builtins "github.com/3rg0n/thlibo/processors"

	"github.com/3rg0n/thlibo/internal/processors"
)

// Every built-in processor in the repo's processors/ directory must
// be reachable from the embedded FS. Catches the "added a new
// processor folder but forgot to extend the //go:embed line"
// regression — has bitten us with `shorthand` and `stacktrace-filter`.
func TestEmbeddedFSContainsAllBuiltins(t *testing.T) {
	r, _, err := processors.BuildFromSources(processors.Source{
		FS:     builtins.FS,
		Origin: processors.OriginBuiltin,
	})
	if err != nil {
		t.Fatalf("BuildFromSources: %v", err)
	}

	want := []string{
		// v0.1 originals
		"git-filter", "npm-filter", "cargo-filter",
		"compress", "casefolder",
		// v0.4 — shorthand prompt processor
		"shorthand",
		// v0.5 — log-processing family
		"stacktrace-filter",
		"pytest-filter",
		"ndjson-filter",
	}
	for _, name := range want {
		if d := r.Get(name); d == nil {
			t.Errorf("built-in processor %q missing from embedded FS — check //go:embed in processors/embed.go", name)
		}
	}
}
