#!/usr/bin/env python3
"""Unit tests for pdf-to-md's fail-open contract on unparseable input.

Run: python -m unittest processors/pdf-to-md/testdata/test_failopen.py

The regression these pin: every error path used to write a two-line
HTML comment to *stdout* and exit 0. Because stdout IS the compressed
tool output, that comment REPLACED the document — a PDF reached the
model as ~200 bytes of "pypdf failed to open document" and the original
bytes were gone. Architectural invariant #2 (ADR 0006) names parse
failure as a pass-through-the-original-bytes condition, and the
dispatcher implements that by treating a non-zero exit as "fall back".

So the contract is: diagnostics on stderr, stdout empty, exit non-zero.
The one deliberate exception is a pdfplumber open failure, where the
document-level pass already produced real markdown worth keeping.
"""

from __future__ import annotations

import importlib.util
import io
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

spec = importlib.util.spec_from_file_location("pdf_run", ROOT / "run.py")
pdf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pdf)


class _Capture:
    """Swap stdout/stderr for text buffers and feed stdin bytes.

    run.py reads sys.stdin.buffer, so the fake stdin needs a .buffer
    attribute holding the raw bytes.
    """

    def __init__(self, raw: bytes):
        self.raw = raw

    def __enter__(self):
        self.out, self.err = io.StringIO(), io.StringIO()
        self._saved = (sys.stdout, sys.stderr, sys.stdin, sys.argv)
        sys.stdout, sys.stderr = self.out, self.err

        class _Stdin:
            buffer = io.BytesIO(self.raw)

        sys.stdin = _Stdin()
        # main() checks argv for --page-count / --render-page.
        sys.argv = ["run.py"]
        return self

    def __exit__(self, *exc):
        sys.stdout, sys.stderr, sys.stdin, sys.argv = self._saved
        return False


class TestFailOpen(unittest.TestCase):
    def test_non_pdf_input_exits_nonzero_with_empty_stdout(self):
        """No %PDF- magic: the middleware must get the fallback signal,
        not a substituted error payload."""
        with _Capture(b"this is plainly not a PDF at all\n") as cap:
            rc = pdf.main()
        self.assertNotEqual(rc, 0, "non-PDF input must signal fallback")
        self.assertEqual(cap.out.getvalue(), "", "stdout must stay empty")
        self.assertIn("does not look like a PDF", cap.err.getvalue())

    def test_corrupt_pdf_exits_nonzero_with_empty_stdout(self):
        """Valid magic, truncated body — the macOS-reported case. pypdf
        raises, and the original bytes must survive."""
        raw = b"%PDF-1.5\n" + b"x" * 3000
        with _Capture(raw) as cap:
            rc = pdf.main()
        self.assertNotEqual(rc, 0, "corrupt PDF must signal fallback")
        self.assertEqual(cap.out.getvalue(), "", "stdout must stay empty")

    def test_emit_error_writes_only_to_stderr(self):
        """The load-bearing detail: anything emit_error puts on stdout
        would be served to the model as the document."""
        with _Capture(b"") as cap:
            pdf.emit_error("boom", b"%PDF-1.5")
        self.assertEqual(cap.out.getvalue(), "")
        err = cap.err.getvalue()
        self.assertIn("boom", err)
        self.assertIn("25 50 44 46", err, "hex preview of the first bytes")


if __name__ == "__main__":
    unittest.main()
