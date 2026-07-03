package processors

import "sync"

// NativeFilter is a deterministic built-in processor implemented in Go
// (ADR 0010). It takes the raw input bytes and returns the processed
// bytes. A filter should NOT enforce the monotonic byte-win guarantee
// or panic-safety itself — RunNative wraps every filter with both, so
// each filter can focus purely on the transform and simply return its
// best output (or the input unchanged when it has nothing to do).
type NativeFilter func(input []byte) []byte

var (
	nativeMu      sync.RWMutex
	nativeFilters = map[string]NativeFilter{}
)

// RegisterNative binds a native filter to a processor name. Called from
// each filter file's init(). Panics on a duplicate name — that's a
// build-time programming error, not a runtime condition.
func RegisterNative(name string, fn NativeFilter) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if _, dup := nativeFilters[name]; dup {
		panic("processors: duplicate native filter registered: " + name)
	}
	nativeFilters[name] = fn
}

// nativeFilter returns the registered filter for name, or nil.
func nativeFilter(name string) NativeFilter {
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	return nativeFilters[name]
}

// RunNative executes the named native filter against input and returns
// the result. It enforces the two invariants every built-in filter must
// honour (ADR 0010), so individual filters don't each reimplement them:
//
//   - Panic safety: a panicking filter must never break the AI client.
//     A recovered panic yields the original input unchanged.
//   - Monotonic guarantee: the compressed form is returned only when it
//     is a strict byte win over the input; otherwise the input is
//     returned verbatim. (Filters may also return input themselves when
//     they don't recognise the shape; this is the outer backstop.)
//
// Returns (output, true) on success, or ("", false) if no filter is
// registered for name — the dispatcher treats the latter as an error
// and the middleware falls back to the original input.
func RunNative(name string, input []byte) (out []byte, ok bool) {
	fn := nativeFilter(name)
	if fn == nil {
		return nil, false
	}
	result := input // default: unchanged, so a panic path is a no-op win
	func() {
		defer func() {
			if r := recover(); r != nil {
				result = input
			}
		}()
		result = fn(input)
	}()
	// Monotonic: only accept a strict byte-count improvement.
	if len(result) >= len(input) {
		return input, true
	}
	return result, true
}
