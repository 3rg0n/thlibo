#!/usr/bin/env python3
"""Unit tests for go-test-filter.

Run from repo root:
    python3 processors/go-test-filter/testdata/test_gotest.py
"""

from __future__ import annotations

import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import run  # noqa: E402


class TestAllPass(unittest.TestCase):
    def test_drops_passing_noise(self):
        raw = "".join(
            f"=== RUN   Test{n}\n--- PASS: Test{n} (0.00s)\n"
            for n in ("A", "B", "C", "D", "E")
        ) + "PASS\nok  \tgithub.com/x/y\t0.042s\n"
        out = run.compress(raw)
        # All the RUN/PASS noise gone; only the package tally remains.
        self.assertNotIn("=== RUN", out)
        self.assertNotIn("--- PASS", out)
        self.assertIn("ok  \tgithub.com/x/y", out)
        self.assertLess(len(out), len(raw))


class TestSingleFailure(unittest.TestCase):
    def test_keeps_failure_detail_in_order(self):
        raw = (
            "=== RUN   TestPass\n--- PASS: TestPass (0.00s)\n"
            "=== RUN   TestFail\n"
            "    foo_test.go:42: got 7, want 9\n"
            "--- FAIL: TestFail (0.00s)\n"
            "FAIL\nFAIL\tgithub.com/x/y\t0.05s\n"
        )
        out = run.compress(raw)
        self.assertIn("foo_test.go:42: got 7, want 9", out)
        self.assertIn("--- FAIL: TestFail", out)
        # detail precedes the FAIL header, matching go's native order
        self.assertLess(out.index("foo_test.go:42"), out.index("--- FAIL: TestFail"))
        # passing test dropped
        self.assertNotIn("TestPass", out)
        self.assertIn("FAIL\tgithub.com/x/y", out)


class TestBuildError(unittest.TestCase):
    def test_build_error_kept_verbatim(self):
        raw = (
            "# github.com/x/y\n"
            "./broken.go:10:2: undefined: fmt.Printlnx\n"
            "FAIL\tgithub.com/x/y [build failed]\n"
        )
        out = run.compress(raw)
        # Build errors are load-bearing — every line kept.
        self.assertIn("# github.com/x/y", out)
        self.assertIn("./broken.go:10:2: undefined: fmt.Printlnx", out)
        self.assertIn("[build failed]", out)


class TestMixedPackages(unittest.TestCase):
    def test_mixed(self):
        raw = (
            "=== RUN   TestA\n--- PASS: TestA (0.00s)\n"
            "ok  \tgithub.com/x/a\t0.01s\n"
            "=== RUN   TestB\n    b_test.go:5: boom\n--- FAIL: TestB (0.00s)\n"
            "FAIL\nFAIL\tgithub.com/x/b\t0.02s\n"
            "?   \tgithub.com/x/c\t[no test files]\n"
        )
        out = run.compress(raw)
        self.assertIn("ok  \tgithub.com/x/a", out)
        self.assertIn("b_test.go:5: boom", out)
        self.assertIn("--- FAIL: TestB", out)
        self.assertIn("FAIL\tgithub.com/x/b", out)
        self.assertIn("[no test files]", out)
        self.assertNotIn("TestA", out)


class TestPanic(unittest.TestCase):
    def test_panic_stack_kept(self):
        raw = (
            "=== RUN   TestPanics\n"
            "panic: runtime error: index out of range [3] with length 2\n"
            "\ngoroutine 6 [running]:\n"
            "github.com/x/y.TestPanics(0x...)\n"
            "\t/src/y_test.go:14 +0x1d\n"
            "FAIL\tgithub.com/x/y\t0.03s\n"
        )
        out = run.compress(raw)
        self.assertIn("panic: runtime error", out)
        self.assertIn("y_test.go:14", out)


class TestJSON(unittest.TestCase):
    def test_json_events(self):
        events = []
        # Many passing tests (the noise that makes distillation a win).
        for k in range(20):
            t = f"TestPass{k}"
            events += [
                f'{{"Action":"run","Test":"{t}"}}',
                f'{{"Action":"output","Test":"{t}","Output":"=== RUN   {t}\\n"}}',
                f'{{"Action":"output","Test":"{t}","Output":"--- PASS: {t} (0.00s)\\n"}}',
                f'{{"Action":"pass","Test":"{t}"}}',
            ]
        events += [
            '{"Action":"run","Test":"TestB"}',
            '{"Action":"output","Test":"TestB","Output":"    b_test.go:5: got X want Y\\n"}',
            '{"Action":"fail","Test":"TestB"}',
            '{"Action":"fail","Package":"github.com/x/y"}',
        ]
        raw = "\n".join(events) + "\n"
        out = run.compress(raw)
        self.assertIn("--- FAIL: TestB", out)
        self.assertIn("b_test.go:5: got X want Y", out)
        self.assertIn("FAIL github.com/x/y", out)
        self.assertNotIn("TestPass", out)  # passing tests dropped
        self.assertLess(len(out), len(raw))


class TestMonotonic(unittest.TestCase):
    def test_passthrough_when_not_smaller(self):
        # A tiny all-fail input where distillation wouldn't shrink:
        # keep everything → output must not exceed input (passthrough).
        raw = "=== RUN   T\n    x_test.go:1: e\n--- FAIL: T (0.00s)\nFAIL\n"
        out = run.compress(raw)
        self.assertLessEqual(len(out.encode()), len(raw.encode()))

    def test_non_gotest_passthrough(self):
        raw = "just some unrelated text\nwith two lines\n"
        self.assertEqual(run.compress(raw), raw)


if __name__ == "__main__":
    unittest.main(verbosity=1)
