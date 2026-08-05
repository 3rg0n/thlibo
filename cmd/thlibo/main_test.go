package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// main() dispatches with os.Exit, which cannot be called in-process
// without killing the test binary, so these tests compile the real
// command once and run it as a subprocess. That also makes them the only
// tests that exercise the actual argv wiring a user hits.

// binCache memoises one build per version tag for the whole package.
// Nine tests over three tags would otherwise mean nine `go build`
// invocations (~100s wall clock); memoised it is three.
var (
	binMu    sync.Mutex
	binCache = map[string]string{}
	binDir   string
)

// buildBinary compiles cmd/thlibo with the given version tag injected
// and returns the path, reusing an earlier build of the same tag. Tag
// matters: an un-injected build reports "dev" and short-circuits the
// update check, which would make the check tests pass for the wrong
// reason.
func buildBinary(t *testing.T, tag string) string {
	t.Helper()

	binMu.Lock()
	defer binMu.Unlock()
	if p, ok := binCache[tag]; ok {
		return p
	}

	name := "thlibo-test-" + tag
	if tag == "" {
		name = "thlibo-test-untagged"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(binDir, name)

	args := []string{"build", "-o", bin}
	if tag != "" {
		args = append(args, "-ldflags", "-X github.com/3rg0n/thlibo/internal/version.Tag="+tag)
	}
	args = append(args, ".")

	cmd := exec.Command("go", args...) // #nosec G204 -- fixed argv, tag is a test literal
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	binCache[tag] = bin
	return bin
}

// TestMain owns the shared build directory: t.TempDir() is per-test and
// would be removed while a later test still held the cached path.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "thlibo-maintest")
	if err != nil {
		panic(err)
	}
	binDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// runBin invokes the built binary with a home dir pointed at a temp
// path, and returns exit code, stdout, stderr.
func runBin(t *testing.T, bin string, env []string, argv ...string) (int, string, string) {
	t.Helper()

	home := t.TempDir()
	cmd := exec.Command(bin, argv...) // #nosec G204 -- bin is our own build output
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"THLIBO_PROCESSORS_DIR="+t.TempDir(),
	)
	cmd.Env = append(cmd.Env, env...)

	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", argv, err)
	}
	return code, out.String(), errb.String()
}

// No arguments is a usage error (2), with the usage text on stderr. Exit
// 0 here would make a bare `thlibo` in a script look like a successful
// no-op.
func TestNoArgsIsUsageError(t *testing.T) {
	bin := buildBinary(t, "")

	code, _, errOut := runBin(t, bin, nil)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "Usage:") {
		t.Errorf("no usage text on stderr:\n%s", errOut)
	}
}

// An unknown subcommand is exit 2 and names the offender, so a typo is
// diagnosable rather than a silent nothing.
func TestUnknownSubcommandIsUsageError(t *testing.T) {
	bin := buildBinary(t, "")

	code, _, errOut := runBin(t, bin, nil, "frobnicate")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown subcommand") || !strings.Contains(errOut, "frobnicate") {
		t.Errorf("stderr does not name the unknown subcommand:\n%s", errOut)
	}
}

// `thlibo pull` was removed in v0.6.0 (model downloads are inferd's
// job). It must stay a *loud* exit 2 that points at `inferd pull`, not
// fall through to "unknown subcommand" — users and old scripts still
// reach for it, and the redirect is the only thing telling them where it
// went.
func TestRemovedPullSubcommandExplainsItself(t *testing.T) {
	bin := buildBinary(t, "")

	code, _, errOut := runBin(t, bin, nil, "pull")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "removed in v0.6.0") {
		t.Errorf("stderr does not explain the removal:\n%s", errOut)
	}
	if !strings.Contains(errOut, "inferd pull") {
		t.Errorf("stderr does not redirect to `inferd pull`:\n%s", errOut)
	}
}

// help is exit 0 (asking for help succeeded) whereas no-args is exit 2.
// All three spellings must land on the same branch.
func TestHelpExitsZero(t *testing.T) {
	bin := buildBinary(t, "")

	for _, argv := range []string{"help", "-h", "--help"} {
		code, _, errOut := runBin(t, bin, nil, argv)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", argv, code)
		}
		if !strings.Contains(errOut, "AI tool output compressor") {
			t.Errorf("%s: no usage text on stderr", argv)
		}
	}
}

// version prints the injected tag on stdout and nothing else — scripts
// parse it. All three spellings agree.
func TestVersionPrintsInjectedTag(t *testing.T) {
	bin := buildBinary(t, "v9.9.9")

	for _, argv := range []string{"version", "-v", "--version"} {
		code, out, _ := runBin(t, bin, nil, argv)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", argv, code)
		}
		if strings.TrimSpace(out) != "v9.9.9" {
			t.Errorf("%s: stdout = %q, want %q", argv, strings.TrimSpace(out), "v9.9.9")
		}
	}
}

// The update-check short-circuit: `version` must not spawn a network
// call. Asserted against a real local server standing in for the GitHub
// API — if the check fires, the server records a hit.
//
// The tag is injected because an un-injected "dev" build skips the check
// unconditionally (Runner.shouldSkipEnv), which would make this pass
// without proving anything about the subcommand branch.
func TestVersionDoesNotTriggerUpdateCheck(t *testing.T) {
	bin := buildBinary(t, "v0.0.1")

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"http://example.invalid"}`))
	}))
	defer srv.Close()

	env := []string{
		"THLIBO_UPDATE_API=" + srv.URL,
		// Force the synchronous path so a check, if it fired, would have
		// completed before the process exited — otherwise a race could
		// hide the request and this test would pass spuriously.
		"THLIBO_HEADLESS=1",
	}

	if code, _, _ := runBin(t, bin, env, "version"); code != 0 {
		t.Fatalf("version exit = %d, want 0", code)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("`thlibo version` made %d update-check request(s); it must not touch the network", n)
	}
}

// The counterpart: a non-version subcommand on a tagged build DOES run
// the check, and in headless mode waits for it. Without this, the test
// above could pass because the check is broken everywhere rather than
// because `version` short-circuits.
func TestOtherSubcommandsDoTriggerUpdateCheck(t *testing.T) {
	bin := buildBinary(t, "v0.0.1")

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"http://example.invalid"}`))
	}))
	defer srv.Close()

	env := []string{
		"THLIBO_UPDATE_API=" + srv.URL,
		"THLIBO_HEADLESS=1",
	}

	// `config --path` is the cheapest real subcommand: no stdin, no
	// daemon, no filesystem mutation beyond the state file the check
	// itself writes.
	if code, _, errOut := runBin(t, bin, env, "config", "--path"); code != 0 {
		t.Fatalf("config --path exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if hits.Load() == 0 {
		t.Fatal("no update-check request fired for a non-version subcommand on a tagged build; the version short-circuit test above would then prove nothing")
	}
}

// THLIBO_NO_UPDATE=1 suppresses the check for every subcommand. This is
// the documented kill switch, and CI relies on it — a regression would
// put a GitHub API call in front of every hook invocation on the runner.
func TestNoUpdateEnvSuppressesCheck(t *testing.T) {
	bin := buildBinary(t, "v0.0.1")

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	}))
	defer srv.Close()

	env := []string{
		"THLIBO_UPDATE_API=" + srv.URL,
		"THLIBO_HEADLESS=1",
		"THLIBO_NO_UPDATE=1",
	}

	if code, _, errOut := runBin(t, bin, env, "config", "--path"); code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("THLIBO_NO_UPDATE=1 still made %d update-check request(s)", n)
	}
}

// Every subcommand in the usage text must actually dispatch. Guards the
// drift that documentation-only changes cause: a subcommand listed in
// usage() but missing from the switch falls through to "unknown
// subcommand", which is a user-visible lie.
//
// `pull` is excluded deliberately — it is documented in usage() as a
// historical artifact and covered by its own removal test above.
func TestDocumentedSubcommandsAllDispatch(t *testing.T) {
	bin := buildBinary(t, "")

	// Invoked with a flag that makes each a no-op where one exists;
	// otherwise with empty stdin. What is asserted is only that the
	// dispatch happened — i.e. stderr does NOT say "unknown
	// subcommand".
	for _, argv := range [][]string{
		{"rewrite"},
		{"exec"},
		{"compress"},
		{"install", "--dry-run", "--skip-inferd"},
		{"uninstall", "--dry-run", "--skip-autostart"},
		{"case", "--prune", "9999h"},
		{"shorthand", "-h"},
		{"upgrade", "-h"},
		{"config", "--path"},
	} {
		_, _, errOut := runBin(t, bin, []string{"THLIBO_NO_UPDATE=1"}, argv...)
		if strings.Contains(errOut, "unknown subcommand") {
			t.Errorf("%q is in usage() but does not dispatch:\n%s", argv[0], errOut)
		}
	}
}
