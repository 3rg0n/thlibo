package middleware

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtins "github.com/3rg0n/thlibo/processors"

	"github.com/3rg0n/thlibo/internal/processors"
)

// v0.5 stage 4 — case-file orchestrator regression.
//
// The "case-file orchestrator" pattern from the post on optimising
// tokens (Python normalize → family detect → compress → assemble)
// is implemented by the existing thlibo case + middleware pipeline:
//
//   thlibo case <file>
//     → casefile.Create
//       → middleware.Pipeline.Process
//         → registry.MatchFastPath  ← family detect via match regex
//           → dispatcher runs the matched script processor
//
// The "orchestration" is the registry's MatchFastPath choosing
// the right family filter by content shape — no router round-trip,
// no daemon needed for the deterministic path. This test confirms
// the v0.5 family filters (stacktrace / pytest / ndjson) wire into
// that flow correctly: each fixture must hit ITS family's filter,
// not git's or npm's by accident.
func TestFamilyDispatchByFastPath(t *testing.T) {
	// Materialise the embedded FS so script processors can chdir
	// + exec their entry files. Same trick TestScriptBuiltinsC6
	// uses; consolidate when we have a third caller.
	diskRoot := t.TempDir()
	if err := copyEmbedTree(builtins.FS, ".", diskRoot); err != nil {
		t.Fatalf("mirror builtins: %v", err)
	}
	reg, _, err := processors.BuildFromDisk(diskRoot, "")
	if err != nil {
		t.Fatalf("BuildFromDisk: %v", err)
	}

	cases := []struct {
		name        string
		fixture     string
		wantFilter  string
	}{
		{"python traceback → stacktrace-filter", pythonRecursionFixture, "stacktrace-filter"},
		{"pytest session → pytest-filter", pytestSessionFixture, "pytest-filter"},
		{"ndjson stream → ndjson-filter", ndjsonRepeatedErrorFixture, "ndjson-filter"},
		{"git status → git-filter", gitStatusFixture, "git-filter"},
		{"npm list → npm-filter", npmListFixture, "npm-filter"},
		{"cargo build → cargo-filter", cargoBuildFixture, "cargo-filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := reg.MatchFastPath(tc.fixture)
			if d == nil {
				t.Fatalf("no filter matched fixture; want %s", tc.wantFilter)
			}
			if d.Name != tc.wantFilter {
				t.Errorf("dispatched to %q, want %q", d.Name, tc.wantFilter)
			}
		})
	}
}

// v0.5 stage 4 — confirm content that doesn't match any family's
// `match` regex falls through to the router (returns nil from
// MatchFastPath). This is the "unknown format" path where the
// daemon's compress processor gets the residue.
func TestUnknownContentFallsThrough(t *testing.T) {
	diskRoot := t.TempDir()
	if err := copyEmbedTree(builtins.FS, ".", diskRoot); err != nil {
		t.Fatalf("mirror builtins: %v", err)
	}
	reg, _, err := processors.BuildFromDisk(diskRoot, "")
	if err != nil {
		t.Fatalf("BuildFromDisk: %v", err)
	}

	prose := strings.Repeat("This is a plain English sentence with no log shape or markers. ", 50)
	if d := reg.MatchFastPath(prose); d != nil {
		t.Errorf("plain prose unexpectedly matched %q; want nil", d.Name)
	}
}

// Recursive-walk helper duplicated from builtins_test.go so this
// file is independently runnable when only the dispatch tests are
// targeted (`go test -run FamilyDispatch`). Keeping it local also
// avoids reaching into builtins_test.go's file-scope fixtures with
// circular package init concerns.
var _ = fs.SkipDir // keep fs import live for the helper signature
var _ = os.PathSeparator
var _ = filepath.Separator
