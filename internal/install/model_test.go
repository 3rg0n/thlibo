package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeModelServer serves a fixed byte payload, supports HTTP Range,
// and optionally simulates mid-stream failures. Lets us exercise the
// full Pull state machine without hitting HuggingFace.
type fakeModelServer struct {
	payload      []byte
	reqCount     atomic.Int32
	cutAfterByte int64 // if > 0, close connection after N bytes of one response
}

func (f *fakeModelServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.reqCount.Add(1)
		data := f.payload
		var start int64
		if rng := r.Header.Get("Range"); rng != "" {
			// "bytes=<start>-" is all we need to parse.
			if !strings.HasPrefix(rng, "bytes=") {
				http.Error(w, "bad range", http.StatusBadRequest)
				return
			}
			s := strings.TrimPrefix(rng, "bytes=")
			s = strings.TrimSuffix(s, "-")
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad range", http.StatusBadRequest)
				return
			}
			start = n
			if start > int64(len(data)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if start == int64(len(data)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			data = data[start:]
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.Itoa(len(f.payload)-1)+"/"+strconv.Itoa(len(f.payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
		}
		if f.cutAfterByte > 0 && int64(len(data)) > f.cutAfterByte {
			_, _ = w.Write(data[:f.cutAfterByte])
			// Simulate abrupt close.
			if hijacker, ok := w.(http.Hijacker); ok {
				if c, _, err := hijacker.Hijack(); err == nil {
					_ = c.Close()
				}
			}
			return
		}
		_, _ = w.Write(data)
	}
}

// newFakeModel spins up a test server + returns the matching Model
// struct pinned to the payload's SHA. Caller must close the server.
func newFakeModel(t *testing.T, payload []byte) (*httptest.Server, Model, *fakeModelServer) {
	t.Helper()
	f := &fakeModelServer{payload: payload}
	srv := httptest.NewServer(f.handler())
	h := sha256.Sum256(payload)
	m := Model{
		Name:           "test-model",
		URL:            srv.URL + "/fake.gguf",
		ExpectedSHA256: hex.EncodeToString(h[:]),
		Filename:       "fake.gguf",
		SizeBytes:      int64(len(payload)),
	}
	return srv, m, f
}

// TestPullFreshDownload: empty dir, happy path. Verifies the file
// lands with the expected bytes and the server was hit exactly once.
func TestPullFreshDownload(t *testing.T) {
	payload := []byte("pretend this is a gguf")
	srv, m, f := newFakeModel(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	path, err := Pull(context.Background(), m, PullOptions{
		Dir:    dir,
		Client: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) {
		t.Errorf("downloaded bytes differ from payload")
	}
	if n := f.reqCount.Load(); n != 1 {
		t.Errorf("server hit %d times, want 1", n)
	}
}

// TestPullIdempotent: running Pull twice should not re-download.
func TestPullIdempotent(t *testing.T) {
	payload := []byte("idempotent payload bytes")
	srv, m, f := newFakeModel(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	opts := PullOptions{Dir: dir, Client: srv.Client()}

	if _, err := Pull(context.Background(), m, opts); err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	if _, err := Pull(context.Background(), m, opts); err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if n := f.reqCount.Load(); n != 1 {
		t.Errorf("server hit %d times after two Pulls, want 1", n)
	}
}

// TestPullSHAFailureCleansUpPartial: a payload whose hash doesn't
// match ExpectedSHA256 is rejected, AND the .part file is removed
// so the next run starts fresh.
func TestPullSHAFailureCleansUpPartial(t *testing.T) {
	srv, m, _ := newFakeModel(t, []byte("legitimate"))
	defer srv.Close()
	m.ExpectedSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	dir := t.TempDir()
	_, err := Pull(context.Background(), m, PullOptions{Dir: dir, Client: srv.Client()})
	if err == nil {
		t.Fatal("expected SHA mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error doesn't mention sha: %v", err)
	}
	// Neither the final file nor the .part should remain.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected dir empty after failure, got %v", entries)
	}
}

// TestPullResumeFromPartial: simulate an interrupted download by
// placing a .part file containing a prefix of the payload, then
// verify Pull completes it via HTTP Range rather than re-downloading.
func TestPullResumeFromPartial(t *testing.T) {
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte('A' + (i % 26))
	}
	srv, m, f := newFakeModel(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	// Seed a 30-byte partial.
	if err := os.WriteFile(filepath.Join(dir, m.Filename+".part"), payload[:30], 0o600); err != nil {
		t.Fatal(err)
	}

	var progressCalls atomic.Int32
	progress := func(written, total int64) { progressCalls.Add(1) }

	path, err := Pull(context.Background(), m, PullOptions{
		Dir:      dir,
		Client:   srv.Client(),
		Progress: progress,
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(payload) {
		t.Errorf("final bytes incorrect after resume")
	}
	if n := f.reqCount.Load(); n != 1 {
		t.Errorf("server hit %d times during resume, want 1", n)
	}
	if progressCalls.Load() == 0 {
		t.Error("progress callback never called")
	}
}

// TestPullRefusesUnpinnedWithoutFlag: DefaultModel has empty
// ExpectedSHA256. Without AllowUnpinned, Pull must refuse.
func TestPullRefusesUnpinnedWithoutFlag(t *testing.T) {
	srv, m, _ := newFakeModel(t, []byte("x"))
	defer srv.Close()
	m.ExpectedSHA256 = "" // simulate unpinned

	dir := t.TempDir()
	_, err := Pull(context.Background(), m, PullOptions{Dir: dir, Client: srv.Client()})
	if err == nil {
		t.Fatal("expected refusal on unpinned SHA")
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Errorf("error doesn't explain the issue: %v", err)
	}
}

// TestPullAllowsUnpinnedWithFlag: AllowUnpinned = true lets the
// download proceed and skip verification.
func TestPullAllowsUnpinnedWithFlag(t *testing.T) {
	payload := []byte("pretend gguf")
	srv, m, _ := newFakeModel(t, payload)
	defer srv.Close()
	m.ExpectedSHA256 = "" // simulate unpinned

	dir := t.TempDir()
	path, err := Pull(context.Background(), m, PullOptions{
		Dir:           dir,
		Client:        srv.Client(),
		AllowUnpinned: true,
	})
	if err != nil {
		t.Fatalf("Pull with AllowUnpinned: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) {
		t.Errorf("payload not written")
	}
}

// TestPullRejectsNonHTTPS: the URL validator catches http:// and
// file:// early.
func TestPullRejectsNonHTTPS(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{
		"http://example.com/x.gguf",
		"file:///etc/passwd",
		"",
	} {
		_, err := Pull(context.Background(), Model{
			Name:           "x",
			URL:            bad,
			ExpectedSHA256: "0",
			Filename:       "x.gguf",
		}, PullOptions{Dir: dir})
		if err == nil {
			t.Errorf("URL %q should have been rejected", bad)
		}
	}
}

// TestPullRangeNotSatisfiable: if the .part file is already the
// full size, the server returns 416 and Pull treats that as done.
// Verification catches the (valid) file.
func TestPullRangeNotSatisfiable(t *testing.T) {
	payload := []byte("complete payload")
	srv, m, _ := newFakeModel(t, payload)
	defer srv.Close()

	dir := t.TempDir()
	// Pre-seed .part with the full payload.
	if err := os.WriteFile(filepath.Join(dir, m.Filename+".part"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := Pull(context.Background(), m, PullOptions{
		Dir:    dir,
		Client: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) {
		t.Errorf("bytes wrong after 416 handling")
	}
}

// TestModelsDirHonorsEnv: THLIBO_MODELS_DIR beats the default.
func TestModelsDirHonorsEnv(t *testing.T) {
	t.Setenv("THLIBO_MODELS_DIR", "/custom/path")
	if got := ModelsDir(); got != "/custom/path" {
		t.Errorf("ModelsDir = %q, want /custom/path", got)
	}
}

// TestValidateModelURL: direct coverage of the validator.
func TestValidateModelURL(t *testing.T) {
	ok := []string{
		"https://huggingface.co/x/y/resolve/main/z.gguf",
		"https://example.com/model.gguf",
	}
	for _, u := range ok {
		if err := validateModelURL(u); err != nil {
			t.Errorf("validate(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{"", "ftp://x", "http://x", "://bad"}
	for _, u := range bad {
		if err := validateModelURL(u); err == nil {
			t.Errorf("validate(%q) = nil, want error", u)
		}
	}
	_ = (&url.URL{}) // silence unused import complaint if any
}
