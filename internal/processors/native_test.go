package processors

import (
	"strings"
	"testing"
)

func init() {
	// Test-only filters registered under names that won't collide with
	// real built-ins (which use kebab names like git-filter).
	RegisterNative("test-halve", func(in []byte) []byte {
		// Return roughly half the input — a strict byte win.
		return in[:len(in)/2]
	})
	RegisterNative("test-grow", func(in []byte) []byte {
		// Return MORE than the input — must be rejected by the monotonic
		// guard (RunNative returns the original instead).
		return append(append([]byte{}, in...), in...)
	})
	RegisterNative("test-panic", func(in []byte) []byte {
		panic("boom")
	})
}

func TestRunNativeMonotonic(t *testing.T) {
	in := []byte("abcdefghij") // 10 bytes
	out, ok := RunNative("test-halve", in)
	if !ok {
		t.Fatal("expected ok")
	}
	if len(out) >= len(in) {
		t.Errorf("halve should shrink: got %d want <%d", len(out), len(in))
	}
}

func TestRunNativeRejectsGrowth(t *testing.T) {
	in := []byte("abcde")
	out, ok := RunNative("test-grow", in)
	if !ok {
		t.Fatal("expected ok")
	}
	if string(out) != string(in) {
		t.Errorf("growth must be rejected -> original returned; got %q", out)
	}
}

func TestRunNativePanicSafe(t *testing.T) {
	in := []byte("original input that must survive a panic")
	out, ok := RunNative("test-panic", in)
	if !ok {
		t.Fatal("expected ok")
	}
	if string(out) != string(in) {
		t.Errorf("panic must yield original input; got %q", out)
	}
}

func TestRunNativeUnknown(t *testing.T) {
	if _, ok := RunNative("no-such-filter", []byte("x")); ok {
		t.Error("unknown native filter should return ok=false")
	}
}

// A user-origin descriptor declaring type:native must be rejected —
// there's no user Go code to bind (ADR 0010).
func TestValidateRejectsUserNative(t *testing.T) {
	d := &Descriptor{Name: "test-halve", Type: KindNative, Origin: Origin{Source: OriginUser}}
	if err := validate(d); err == nil {
		t.Error("user-origin type:native must be rejected")
	} else if !strings.Contains(err.Error(), "built-in only") {
		t.Errorf("unexpected error: %v", err)
	}
}

// A built-in native descriptor whose name has no registered filter is
// rejected.
func TestValidateRejectsUnknownNative(t *testing.T) {
	d := &Descriptor{Name: "not-registered", Type: KindNative, Origin: Origin{Source: OriginBuiltin}}
	if err := validate(d); err == nil {
		t.Error("native descriptor with no registered filter must be rejected")
	}
}

// A built-in native descriptor bound to a registered filter validates.
func TestValidateAcceptsBuiltinNative(t *testing.T) {
	d := &Descriptor{Name: "test-halve", Type: KindNative, Origin: Origin{Source: OriginBuiltin}}
	if err := validate(d); err != nil {
		t.Errorf("valid built-in native rejected: %v", err)
	}
}

// A native descriptor must not carry an entry.
func TestValidateRejectsNativeWithEntry(t *testing.T) {
	d := &Descriptor{Name: "test-halve", Type: KindNative, Entry: "run.py", Origin: Origin{Source: OriginBuiltin}}
	if err := validate(d); err == nil {
		t.Error("native descriptor with an entry must be rejected")
	}
}
