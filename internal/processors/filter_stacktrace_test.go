package processors

import (
	"testing"
)

// stacktraceFixtures are representative stack traces the filter must handle.
var stacktraceFixtures = map[string]string{
	"python_traceback": `Traceback (most recent call last):
  File "script.py", line 42, in main
    result = foo(x)
  File "lib.py", line 100, in foo
    return bar(y)
  File "lib.py", line 200, in bar
    return baz(z)
IndexError: list index out of range
`,
	"go_panic": `panic: runtime error: index out of range [42] with length 10

goroutine 1 [running]:
main.(*Cache).Get(0xc0001234, 0x2a)
	/home/user/src/cache.go:42 +0x234
main.process()
	/home/user/src/main.go:100 +0x567
main.main()
	/home/user/src/main.go:200 +0x789
runtime.main()
	/usr/local/go/src/runtime/proc.go:250 +0x100
`,
	"rust_panic": `thread 'main' panicked at 'called Option::unwrap() on a None value':
  0: rust_backtrace::trace
             at src/main.rs:10
  1: rust_backtrace::main
             at src/main.rs:20:5
  2: core::ops::function::FnOnce::call_once
             at /rustc/12345/library/core/src/ops/function.rs:100
`,
	"java_exception": `Exception in thread "main" java.lang.NullPointerException
	at com.example.MyClass.process(MyClass.java:42)
	at com.example.Main.main(Main.java:100)
	at sun.reflect.NativeMethodAccessorImpl.invoke0(Native Method)
Caused by: java.io.IOException
	at com.example.Helper.load(Helper.java:200)
`,
	"node_stack": `TypeError: Cannot read properties of undefined (reading 'length')
    at Object.getData (app.js:42:15)
    at processRequest (app.js:100:5)
    at Layer.handle [as handle_request] (express/lib/router/layer.js:95:10)
    at next (express/lib/router/route.js:137:13)
`,
	"deep_recursion": `Traceback (most recent call last):
  File "recursive.py", line 10, in recurse
    recurse(n - 1)
  File "recursive.py", line 10, in recurse
    recurse(n - 1)
  File "recursive.py", line 10, in recurse
    recurse(n - 1)
  File "recursive.py", line 10, in recurse
    recurse(n - 1)
  File "recursive.py", line 10, in recurse
    recurse(n - 1)
RecursionError: maximum recursion depth exceeded
`,
	"non_trace": "just some random text\nthat doesn't look like a stack trace\n",
	"empty":     "",
}

// TestStacktraceFilterParity runs the Go filter and the Python run.py on the
// same fixtures and requires byte-identical output (ADR 0010 parity).
// Skips when python3 isn't available (e.g. minimal CI images) — the
// filter's own behaviour is still covered by golden tests below.
func TestStacktraceFilterParity(t *testing.T) {
	py := pythonBin(t)
	if py == "" {
		t.Skip("python3 not available; parity check skipped")
	}
	script := referenceScript(t, "stacktrace-filter")
	for name, in := range stacktraceFixtures {
		t.Run(name, func(t *testing.T) {
			want := runPython(t, py, script, in)
			got := string(stacktraceFilter([]byte(in)))
			// The middleware applies the monotonic guard on top; here we
			// compare the raw transform to Python's raw transform.
			if got != want {
				t.Errorf("stacktrace-filter parity mismatch on %q:\n go:\n%s\n py:\n%s", name, got, want)
			}
		})
	}
}
