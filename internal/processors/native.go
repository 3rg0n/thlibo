package processors

import (
	"context"
	"sync"
)

// NativeFilter is a deterministic built-in processor implemented in Go
// (ADR 0010). It takes the raw input bytes and returns the processed
// bytes. A filter should NOT enforce the monotonic byte-win guarantee
// or panic-safety itself — RunNative wraps every filter with both, so
// each filter can focus purely on the transform and simply return its
// best output (or the input unchanged when it has nothing to do).
type NativeFilter func(input []byte) []byte

// NativeCtxFilter is a native filter that needs a context: it makes a
// bounded call out of the process (cordon-filter's embedding round-trip)
// and must abandon it when the caller's deadline expires. Everything
// RunNative guarantees a NativeFilter — panic safety, the monotonic
// byte-win guard — applies identically here.
//
// Most filters must NOT be this. A filter that only transforms bytes has
// no reason to reach the network, and the ctx-free signature is what says
// so at the type level.
type NativeCtxFilter func(ctx context.Context, input []byte) []byte

var (
	nativeMu sync.RWMutex
	// One registry, not two: a name registered ctx-free and ctx-aware
	// would otherwise be a silent shadow rather than the duplicate panic
	// below. RegisterNative adapts its filter into this shape.
	nativeFilters = map[string]NativeCtxFilter{}
)

// RegisterNative binds a native filter to a processor name. Called from
// each filter file's init(). Panics on a duplicate name — that's a
// build-time programming error, not a runtime condition.
func RegisterNative(name string, fn NativeFilter) {
	RegisterNativeCtx(name, func(_ context.Context, in []byte) []byte { return fn(in) })
}

// RegisterNativeCtx binds a context-taking native filter. Same duplicate
// rule as RegisterNative — the two share one namespace.
func RegisterNativeCtx(name string, fn NativeCtxFilter) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if _, dup := nativeFilters[name]; dup {
		panic("processors: duplicate native filter registered: " + name)
	}
	nativeFilters[name] = fn
}

// nativeFilter returns the registered filter for name, or nil.
func nativeFilter(name string) NativeCtxFilter {
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	return nativeFilters[name]
}

// RunNative executes the named native filter against input and returns
// the result. Equivalent to RunNativeCtx with a background context; kept
// for the filters that never look at one.
func RunNative(name string, input []byte) (out []byte, ok bool) {
	return RunNativeCtx(context.Background(), name, input)
}

// RunNativeCtx executes the named native filter against input and
// returns the result. It enforces the two invariants every built-in
// filter must honour (ADR 0010), so individual filters don't each
// reimplement them:
//
//   - Panic safety: a panicking filter must never break the AI client.
//     A recovered panic yields the original input unchanged.
//   - Monotonic guarantee: the compressed form is returned only when it
//     is a strict byte win over the input; otherwise the input is
//     returned verbatim. (Filters may also return input themselves when
//     they don't recognise the shape; this is the outer backstop.)
//
// The context is passed to the filter and is only consulted by filters
// that make bounded calls out of the process. It does NOT abort a
// compute-bound filter mid-transform — a returning filter is the only
// thing that ends the call, so any filter that can loop over
// file-controlled structure must bound that loop itself.
//
// Returns (output, true) on success, or ("", false) if no filter is
// registered for name — the dispatcher treats the latter as an error
// and the middleware falls back to the original input.
func RunNativeCtx(ctx context.Context, name string, input []byte) (out []byte, ok bool) {
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
		result = fn(ctx, input)
	}()
	// Monotonic: only accept a strict byte-count improvement.
	if len(result) >= len(input) {
		return input, true
	}
	return result, true
}
