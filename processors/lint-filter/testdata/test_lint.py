#!/usr/bin/env python3
"""Unit tests for lint-filter v0.7.2.

Output schema is TSV: severity-letter \\t loc \\t rule \\t msg.
Help-row continuations are marked with `-` in the severity column.
The compress() function carries a monotonic guarantee: distilled output
must beat raw byte count, otherwise raw is returned verbatim.

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


def _rows(out: str) -> list[str]:
    return [ln for ln in out.splitlines() if ln.strip()]


def _findings(out: str) -> list[str]:
    """TSV rows whose severity column is a real letter (not `-`)."""
    return [ln for ln in _rows(out) if not ln.startswith("-\t")]


# ---- terse single-line shapes (one line in, one row out) -------------

class TestGCCClang(unittest.TestCase):
    def test_basic_warning(self):
        # Use enough findings to clear the monotonic guarantee.
        raw = "".join(
            f"src/test.c:{i}:9: warning: unused variable 'x' [-Wunused-variable]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertTrue(all(r.startswith("W\t") for r in rows))
        self.assertIn("src/test.c:1:9", rows[0])
        self.assertIn("-Wunused-variable", rows[0])
        self.assertIn("unused variable 'x'", rows[0])

    def test_error_sorts_before_warning(self):
        raw = (
            "src/a.c:1:9: warning: unused variable 'x' [-Wunused-variable]\n"
            "src/a.c:2:9: warning: unused variable 'y' [-Wunused-variable]\n"
            "src/a.c:3:9: warning: unused variable 'z' [-Wunused-variable]\n"
            "src/a.c:5:1: error: 'q' is used uninitialized [-Wuninitialized]\n"
            "src/a.c:6:1: error: 'r' is used uninitialized [-Wuninitialized]\n"
            "src/a.c:7:1: error: 's' is used uninitialized [-Wuninitialized]\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        # error group must sort first
        self.assertTrue(rows[0].startswith("E\t"))
        self.assertTrue(rows[-1].startswith("W\t"))


class TestClippyShort(unittest.TestCase):
    def test_clippy_lint_name(self):
        raw = "".join(
            f"src/main.rs:{i}:13: warning: this expression creates a reference [clippy::needless_borrow]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("clippy::needless_borrow", rows[0])


class TestShellcheck(unittest.TestCase):
    def test_shellcheck_default(self):
        raw = "".join(
            f"scripts/{c}.sh:5:12: warning: Quote this to prevent word splitting. [SC2086]\n"
            for c in "abcde"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertTrue(all(r.startswith("W\t") for r in rows))
        self.assertIn("SC2086", rows[0])


class TestEslintTerse(unittest.TestCase):
    def test_compact_format(self):
        raw = "".join(
            f"src/t{i}.js: line {i}, col 7, error - 'unused' is assigned but never used (no-unused-vars)\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertTrue(all(r.startswith("E\t") for r in rows))
        self.assertIn("no-unused-vars", rows[0])

    def test_unix_format(self):
        raw = "".join(
            f"src/t{i}.js:{i}:7: 'unused' is assigned but never used [Error/no-unused-vars]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("no-unused-vars", rows[0])


class TestGolangciLint(unittest.TestCase):
    def test_basic(self):
        raw = "".join(
            f"cmd/m{i}.go:12:2: undefined: fmt (typecheck)\n" for i in range(1, 8)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 7)
        self.assertIn("typecheck", rows[0])

    def test_groups_by_linter(self):
        raw = (
            "cmd/a.go:12:2: undefined: fmt (typecheck)\n"
            "cmd/b.go:5:8: assigned but not used: err (errcheck)\n"
            "cmd/c.go:9:1: undefined: log (typecheck)\n"
            "cmd/d.go:9:2: undefined: log (typecheck)\n"
            "cmd/e.go:5:8: assigned but not used: err (errcheck)\n"
            "cmd/f.go:9:1: undefined: log (typecheck)\n"
        )
        out = run.compress(raw)
        # typecheck (4) sorts before errcheck (2)
        rows = _findings(out)
        self.assertEqual(len(rows), 6)
        self.assertIn("typecheck", rows[0])
        self.assertIn("errcheck", rows[-1])


class TestFlakeRuff(unittest.TestCase):
    def test_flake_codes(self):
        raw = (
            "src/x.py:5:1: E302 expected 2 blank lines, found 1\n"
            "src/x.py:6:1: E302 expected 2 blank lines, found 1\n"
            "src/x.py:12:12: W291 trailing whitespace\n"
            "src/x.py:13:12: W291 trailing whitespace\n"
            "src/x.py:8:3: F841 local variable assigned but never used\n"
            "src/x.py:9:3: F841 local variable assigned but never used\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 6)
        for code in ("E302", "W291", "F841"):
            self.assertIn(code, out)


class TestMypy(unittest.TestCase):
    def test_no_column(self):
        # mypy emits no col → loc should be just file:line, not file:line:0
        raw = "".join(
            f"src/x.py:{i}: error: Incompatible types in assignment [assignment]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("assignment", rows[0])
        self.assertIn("\tsrc/x.py:1\t", rows[0])
        self.assertNotIn("\tsrc/x.py:1:", rows[0])


class TestRubocop(unittest.TestCase):
    def test_letter_severity(self):
        raw = "".join(
            f"app/m{i}.rb:12:10: C: Naming/VariableName: Use snake_case for variable names.\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("Naming/VariableName", rows[0])
        # rubocop's "C" → "convention" → "C" letter
        self.assertTrue(rows[0].startswith("C\t"))

    def test_correctable_tag(self):
        raw = "".join(
            f"app/m{i}.rb:12:10: C: [Correctable] Naming/VariableName: Use snake_case.\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("Naming/VariableName", rows[0])


class TestStylelint(unittest.TestCase):
    def test_basic(self):
        raw = "".join(
            f"styles/m{i}.css:5:2: warning  Expected indentation of 2 spaces [indentation]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("indentation", rows[0])


class TestTsc(unittest.TestCase):
    def test_tsc_parens(self):
        # tsc default: file(line,col): sev TSxxxx: msg
        raw = "".join(
            f"src/m{i}.ts({i},5): error TS2304: Cannot find name 'foo'.\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)
        self.assertIn("TS2304", rows[0])
        self.assertIn("Cannot find name", rows[0])
        self.assertTrue(rows[0].startswith("E\t"))


# ---- verbose multi-line shapes (block in, single row out) ------------

class TestRustcVerbose(unittest.TestCase):
    def test_clippy_multiline_block(self):
        raw = (
            "warning: this expression creates a reference which is immediately dereferenced by the compiler\n"
            "  --> src/main.rs:14:13\n"
            "   |\n"
            "14 |     foo(&&x);\n"
            "   |             ^^^ help: change this to: `&x`\n"
            "   |\n"
            "   = note: `#[warn(clippy::needless_borrow)]` on by default\n"
            "\n"
            "warning: this expression creates a reference\n"
            "  --> src/main.rs:18:5\n"
            "   = note: `#[warn(clippy::needless_borrow)]` on by default\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        # Both findings collapse under one rule group
        self.assertEqual(len(rows), 2)
        self.assertIn("clippy::needless_borrow", rows[0])
        self.assertIn("src/main.rs:14:13", rows[0])

    def test_rustc_error_code_in_brackets(self):
        # rustc uses `error[E0382]:` style
        raw = (
            "error[E0382]: borrow of moved value\n"
            "  --> src/main.rs:5:1\n"
            "   |\n"
            "5  | bar(x)\n"
            "   | ^^^\n"
            "\n"
            "error[E0382]: borrow of moved value\n"
            "  --> src/main.rs:9:1\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 2)
        self.assertTrue(rows[0].startswith("E\t"))
        self.assertIn("E0382", rows[0])

    def test_help_continuation_emitted(self):
        # `= help: foo` becomes a `-`-prefixed continuation row
        raw = (
            "warning: needless borrow\n"
            "  --> src/main.rs:14:13\n"
            "   |\n"
            "14 |     foo(&&x);\n"
            "   |\n"
            "   = help: change this to: `&x`\n"
            "   = note: `#[warn(clippy::needless_borrow)]` on by default\n"
            "\n"
            "warning: needless borrow\n"
            "  --> src/main.rs:18:5\n"
            "   = help: change this to: `&y`\n"
            "   = note: `#[warn(clippy::needless_borrow)]` on by default\n"
        )
        out = run.compress(raw)
        rows = _rows(out)
        # 2 finding rows + 2 help-continuation rows
        finds = [r for r in rows if not r.startswith("-\t")]
        helps = [r for r in rows if r.startswith("-\t")]
        self.assertEqual(len(finds), 2)
        self.assertEqual(len(helps), 2)
        self.assertIn("help: change this to: `&x`", helps[0])


class TestRuffVerbose(unittest.TestCase):
    def test_ruff_default_block(self):
        # Ruff default verbose: `CODE [*] msg` + `--> file:line:col` +
        # source-snippet + caret + `help: ...`
        raw = (
            "E401 [*] Multiple imports on one line\n"
            " --> src/py/demo.py:2:1\n"
            "  |\n"
            "1 | \"\"\"docstring\"\"\"\n"
            "2 | import os, sys\n"
            "  | ^^^^^^^^^^^^^^\n"
            "  |\n"
            "help: Split imports\n"
            "\n"
            "F401 [*] `os` imported but unused\n"
            " --> src/py/demo.py:2:8\n"
            "  |\n"
            "2 | import os, sys\n"
            "  |        ^^\n"
            "  |\n"
            "help: Remove unused import\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 2)
        self.assertIn("E401", out)
        self.assertIn("F401", out)
        helps = [r for r in _rows(out) if r.startswith("-\t")]
        self.assertTrue(any("Split imports" in h for h in helps))


class TestGCCVerbose(unittest.TestCase):
    def test_drops_source_and_caret(self):
        # gcc verbose default: finding line + indented source line + caret
        raw = (
            "src/test.c:2:9: warning: unused variable 'x' [-Wunused-variable]\n"
            "    2 |     int x;\n"
            "      |         ^\n"
            "src/test.c:3:9: warning: unused variable 'y' [-Wunused-variable]\n"
            "    3 |     int y;\n"
            "      |         ^\n"
            "src/test.c:4:9: warning: unused variable 'z' [-Wunused-variable]\n"
            "    4 |     int z;\n"
            "      |         ^\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        # 3 findings, no source/caret rows
        self.assertEqual(len(rows), 3)
        self.assertNotIn("|", out.split("\n")[0])  # first finding row has no `|`
        self.assertNotIn("^", out)


class TestEslintStylish(unittest.TestCase):
    def test_stylish_file_header_plus_rows(self):
        # eslint stylish: bare file path on its own line, then indented rows
        raw = (
            "/Users/foo/src/a.js\n"
            "  2:7   error    'x' is assigned but never used  no-unused-vars\n"
            "  4:1   warning  Missing semicolon               semi\n"
            "\n"
            "/Users/foo/src/b.js\n"
            "  9:3   error    'y' is assigned but never used  no-unused-vars\n"
            "\n"
            "✖ 3 problems (2 errors, 1 warning)\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 3)
        self.assertIn("no-unused-vars", out)
        self.assertIn("semi", out)
        self.assertIn("/Users/foo/src/a.js", rows[0])

    def test_windows_style_path(self):
        raw = (
            "C:\\dev\\src\\a.js\n"
            "  2:7   error    'x' is unused  no-unused-vars\n"
            "  3:7   error    'y' is unused  no-unused-vars\n"
            "  4:7   error    'z' is unused  no-unused-vars\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 3)
        self.assertIn("C:\\dev\\src\\a.js", rows[0])


# ---- monotonic guarantee --------------------------------------------

class TestMonotonic(unittest.TestCase):
    def test_tiny_input_passes_through(self):
        # 74-byte staticcheck-style input would expand under TSV
        # because of letter mapping + reorganization. Must passthrough.
        raw = "demo.go:14:8: argument should be of type T (staticcheck)\n"
        out = run.compress(raw)
        # Either output is identical to raw, or it's strictly smaller.
        self.assertLessEqual(
            len(out.encode("utf-8")), len(raw.encode("utf-8"))
        )

    def test_unparseable_passes_through(self):
        raw = "Hello\nThis isn't lint output\nNothing to see here.\n"
        out = run.compress(raw)
        self.assertEqual(out, raw)

    def test_short_terse_does_not_grow(self):
        # mypy single line with [code]
        raw = "src/x.py:5: error: Incompatible types [assignment]\n"
        out = run.compress(raw)
        self.assertLessEqual(
            len(out.encode("utf-8")), len(raw.encode("utf-8"))
        )

    def test_empty_input(self):
        self.assertEqual(run.compress(""), "")


# ---- ANSI / mixed buffers --------------------------------------------

class TestAnsiStripped(unittest.TestCase):
    def test_ansi_codes_stripped(self):
        raw = "".join(
            f"src/test.c:{i}:9: \x1b[33mwarning:\x1b[0m unused variable 'x' [-Wunused-variable]\n"
            for i in range(1, 6)
        )
        out = run.compress(raw)
        self.assertNotIn("\x1b", out)
        rows = _findings(out)
        self.assertEqual(len(rows), 5)


class TestMixed(unittest.TestCase):
    def test_mixed_terse_buffer(self):
        # CI scenario: one buffer aggregating multiple terse linters.
        # Make it large enough to clear monotonic check.
        raw = "".join(
            f"src/x.py:{i}:1: E302 expected 2 blank lines, found 1\n"
            f"src/x.py:{i}: error: Incompatible types [assignment]\n"
            f"cmd/m{i}.go:12:2: undefined: fmt (typecheck)\n"
            f"scripts/s{i}.sh:5:12: warning: Quote this. [SC2086]\n"
            for i in range(1, 4)
        )
        out = run.compress(raw)
        for rule in ("E302", "assignment", "typecheck", "SC2086"):
            self.assertIn(rule, out)


# ---- env knobs -------------------------------------------------------

class TestCap(unittest.TestCase):
    def test_no_cap_by_default(self):
        # Default MAX_PER_RULE=0 means no cap
        raw = "".join(
            f"src/x.py:{i}:1: E302 expected 2 blank lines, found 1\n"
            for i in range(1, 9)
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 8)
        self.assertNotIn("more", out)

    def test_cap_via_env(self):
        import importlib
        os.environ["LINT_MAX_PER_RULE"] = "2"
        importlib.reload(run)
        try:
            raw = "".join(
                f"src/x.py:{i}:1: E302 blank lines\n" for i in range(1, 6)
            )
            out = run.compress(raw)
            finds = _findings(out)
            self.assertEqual(len(finds), 2)
            self.assertIn("+3 more", out)
        finally:
            os.environ.pop("LINT_MAX_PER_RULE", None)
            importlib.reload(run)


if __name__ == "__main__":
    unittest.main(verbosity=2)
