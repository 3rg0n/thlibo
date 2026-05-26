#!/usr/bin/env python3
"""Unit tests for lint-filter's per-format parsers.

Each test feeds a representative fixture for one tool's default output,
asserts the parser identifies findings, groups them, and respects the
cap. Plain-text-only inputs must pass through unchanged.

Run from repo root:
    python processors/lint-filter/testdata/test_lint.py
"""

from __future__ import annotations

import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import run  # noqa: E402


def _findings_only(out: str) -> list[str]:
    """Strip the trailing tally + passthrough block; keep the finding rows."""
    lines = out.splitlines()
    findings: list[str] = []
    for ln in lines:
        if ln.startswith("lint="):
            break
        if ln.strip():
            findings.append(ln)
    return findings


class TestGCCClang(unittest.TestCase):
    def test_basic_warning(self):
        raw = "src/test.c:2:9: warning: unused variable 'x' [-Wunused-variable]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("warning", rows[0])
        self.assertIn("src/test.c:2:9", rows[0])
        self.assertIn("-Wunused-variable", rows[0])
        self.assertIn("unused variable 'x'", rows[0])

    def test_error_and_warning_sorted(self):
        raw = (
            "src/test.c:2:9: warning: unused variable 'x' [-Wunused-variable]\n"
            "src/test.c:5:1: error: 'y' is used uninitialized [-Wuninitialized]\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 2)
        # error must sort first
        self.assertIn("error", rows[0])
        self.assertIn("warning", rows[1])

    def test_drops_caret_context(self):
        raw = (
            "src/test.c:2:9: warning: unused variable 'x' [-Wunused-variable]\n"
            "    2 |     int x;\n"
            "      |         ^\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        # The `2 |` and `^` lines are context — must not appear as findings.
        self.assertEqual(len(rows), 1)


class TestClippy(unittest.TestCase):
    def test_clippy_lint_name(self):
        raw = "src/main.rs:14:13: warning: this expression creates a reference [clippy::needless_borrow]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("clippy::needless_borrow", rows[0])


class TestShellcheck(unittest.TestCase):
    def test_shellcheck_default(self):
        raw = "scripts/build.sh:5:12: warning: Quote this to prevent word splitting. [SC2086]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("SC2086", rows[0])
        self.assertIn("warning", rows[0])

    def test_shellcheck_groups_same_rule(self):
        raw = (
            "scripts/a.sh:5:12: warning: Quote this. [SC2086]\n"
            "scripts/b.sh:7:3: warning: Quote this. [SC2086]\n"
            "scripts/c.sh:9:1: warning: Quote this. [SC2086]\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        # All 3 fit under default cap of 5.
        self.assertEqual(len(rows), 3)
        self.assertIn("SC2086", out)


class TestEslint(unittest.TestCase):
    def test_compact_format(self):
        raw = "src/t.js: line 2, col 7, error - 'unused' is assigned but never used (no-unused-vars)\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("no-unused-vars", rows[0])
        self.assertIn("error", rows[0])

    def test_unix_format(self):
        raw = "src/t.js:2:7: 'unused' is assigned but never used [Error/no-unused-vars]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("no-unused-vars", rows[0])


class TestGolangciLint(unittest.TestCase):
    def test_basic(self):
        raw = "cmd/main.go:12:2: undefined: fmt (typecheck)\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("typecheck", rows[0])

    def test_groups_by_linter(self):
        raw = (
            "cmd/a.go:12:2: undefined: fmt (typecheck)\n"
            "cmd/b.go:5:8: assigned but not used: err (errcheck)\n"
            "cmd/c.go:9:1: undefined: log (typecheck)\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 3)
        # Two distinct linters → two rule groups
        self.assertIn("typecheck", out)
        self.assertIn("errcheck", out)


class TestFlakeRuff(unittest.TestCase):
    def test_flake_codes(self):
        raw = (
            "src/x.py:5:1: E302 expected 2 blank lines, found 1\n"
            "src/x.py:12:12: W291 trailing whitespace\n"
            "src/x.py:8:3: F841 local variable assigned but never used\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 3)
        self.assertIn("E302", out)
        self.assertIn("W291", out)
        self.assertIn("F841", out)


class TestMypy(unittest.TestCase):
    def test_no_column(self):
        raw = "src/x.py:5: error: Incompatible types in assignment [assignment]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("assignment", rows[0])
        # mypy emits no col → loc should be just file:line
        self.assertIn("src/x.py:5", rows[0])
        self.assertNotIn("src/x.py:5:", rows[0])


class TestRubocop(unittest.TestCase):
    def test_letter_severity(self):
        raw = "app/m.rb:12:10: C: Naming/VariableName: Use snake_case for variable names.\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("Naming/VariableName", rows[0])

    def test_correctable_tag(self):
        raw = "app/m.rb:12:10: C: [Correctable] Naming/VariableName: Use snake_case.\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("Naming/VariableName", rows[0])


class TestStylelint(unittest.TestCase):
    def test_basic(self):
        raw = "styles/m.css:5:2: warning  Expected indentation of 2 spaces [indentation]\n"
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertIn("indentation", rows[0])


class TestCap(unittest.TestCase):
    def test_cap_elides_extras(self):
        # 8 hits of the same rule with default cap=5 → 5 + 1 elision row.
        raw = "".join(
            f"src/x.py:{i}:1: E302 expected 2 blank lines, found 1\n"
            for i in range(1, 9)
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        # 5 finding rows + 1 elision row
        self.assertEqual(len(rows), 6)
        self.assertIn("+3 more", out)
        self.assertIn("lint=8 findings", out)


class TestAnsiStripped(unittest.TestCase):
    def test_ansi_codes_stripped(self):
        raw = (
            "src/test.c:2:9: \x1b[33mwarning:\x1b[0m unused variable 'x' [-Wunused-variable]\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 1)
        self.assertNotIn("\x1b", out)


class TestPassthrough(unittest.TestCase):
    def test_no_findings_returns_input(self):
        raw = "Hello\nThis isn't lint output\nNothing to see here.\n"
        out = run.compress(raw)
        self.assertEqual(out, raw)

    def test_summary_lines_passed_through(self):
        # ESLint emits a summary at the end. It doesn't parse as a
        # finding but should still reach the AI.
        raw = (
            "src/t.js:2:7: 'x' is unused [Error/no-unused-vars]\n"
            "\n"
            "✖ 1 problem (1 error, 0 warnings)\n"
        )
        out = run.compress(raw)
        self.assertIn("no-unused-vars", out)
        self.assertIn("✖ 1 problem", out)


class TestMixed(unittest.TestCase):
    def test_mixed_tools_in_one_buffer(self):
        # Realistic CI scenario: one buffer aggregating multiple linters.
        raw = (
            "src/x.py:5:1: E302 expected 2 blank lines, found 1\n"
            "src/x.py:5: error: Incompatible types [assignment]\n"
            "cmd/m.go:12:2: undefined: fmt (typecheck)\n"
            "scripts/s.sh:5:12: warning: Quote this. [SC2086]\n"
        )
        out = run.compress(raw)
        rows = _findings_only(out)
        self.assertEqual(len(rows), 4)
        # Each rule appears once
        for rule in ("E302", "assignment", "typecheck", "SC2086"):
            self.assertIn(rule, out)


class TestEnvKnobs(unittest.TestCase):
    def test_max_per_rule_env(self):
        # Reload module with override.
        import importlib
        os.environ["LINT_MAX_PER_RULE"] = "2"
        importlib.reload(run)
        try:
            raw = "".join(
                f"src/x.py:{i}:1: E302 blank lines\n" for i in range(1, 6)
            )
            out = run.compress(raw)
            rows = _findings_only(out)
            self.assertEqual(len(rows), 3)  # 2 findings + 1 elision row
            self.assertIn("+3 more", out)
        finally:
            os.environ.pop("LINT_MAX_PER_RULE", None)
            importlib.reload(run)


if __name__ == "__main__":
    unittest.main(verbosity=2)
