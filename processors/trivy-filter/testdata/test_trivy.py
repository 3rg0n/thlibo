#!/usr/bin/env python3
"""Unit tests for trivy-filter."""

from __future__ import annotations

import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import run  # noqa: E402


def _findings(out: str) -> list[str]:
    return [ln for ln in out.splitlines() if ln.strip()]


SAMPLE = (
    "Report Summary\n"
    "\n"
    "┌──────────────────┬──────┬─────────────────┐\n"
    "│      Target      │ Type │ Vulnerabilities │\n"
    "├──────────────────┼──────┼─────────────────┤\n"
    "│ requirements.txt │ pip  │       2         │\n"
    "└──────────────────┴──────┴─────────────────┘\n"
    "\n"
    "requirements.txt (pip)\n"
    "======================\n"
    "Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 0, HIGH: 1, CRITICAL: 1)\n"
    "\n"
    "┌──────────┬────────────────┬──────────┬────────┬───────────────────┬────────────────┬──────────────────────────────┐\n"
    "│ Library  │ Vulnerability  │ Severity │ Status │ Installed Version │ Fixed Version  │            Title             │\n"
    "├──────────┼────────────────┼──────────┼────────┼───────────────────┼────────────────┼──────────────────────────────┤\n"
    "│ django   │ CVE-2019-14234 │ CRITICAL │ fixed  │ 2.2.1             │ 1.11.23, 2.2.4 │ Django: SQL injection in     │\n"
    "│          │                │          │        │                   │                │ JSONField/HStoreField        │\n"
    "│          │                │          │        │                   │                │ https://example.com/cve      │\n"
    "├──────────┼────────────────┼──────────┤        ├───────────────────┼────────────────┼──────────────────────────────┤\n"
    "│ pyyaml   │ CVE-2019-20477 │ HIGH     │        │ 5.1               │ 5.2            │ PyYAML: command execution    │\n"
    "└──────────┴────────────────┴──────────┴────────┴───────────────────┴────────────────┴──────────────────────────────┘\n"
)


class TestBasic(unittest.TestCase):
    def test_two_findings_extracted(self):
        out = run.compress(SAMPLE)
        rows = _findings(out)
        self.assertEqual(len(rows), 2)
        self.assertTrue(rows[0].startswith("C\t"))
        self.assertTrue(rows[1].startswith("H\t"))

    def test_lib_at_version_format(self):
        out = run.compress(SAMPLE)
        rows = _findings(out)
        self.assertIn("django@2.2.1", rows[0])
        self.assertIn("pyyaml@5.1", rows[1])

    def test_cve_id_present(self):
        out = run.compress(SAMPLE)
        self.assertIn("CVE-2019-14234", out)
        self.assertIn("CVE-2019-20477", out)

    def test_title_wrap_merged(self):
        out = run.compress(SAMPLE)
        rows = _findings(out)
        # Wrap merged into one cell
        self.assertIn("JSONField/HStoreField", rows[0])
        self.assertIn("Django: SQL injection in JSONField/HStoreField", rows[0])

    def test_url_dropped_from_title(self):
        out = run.compress(SAMPLE)
        self.assertNotIn("https://example.com", out)

    def test_severity_sort(self):
        out = run.compress(SAMPLE)
        rows = _findings(out)
        # CRITICAL must precede HIGH
        self.assertTrue(rows[0].startswith("C\t"))
        self.assertTrue(rows[1].startswith("H\t"))


class TestPassthrough(unittest.TestCase):
    def test_no_table_returns_input(self):
        raw = "Hello\nThis isn't trivy output\n"
        self.assertEqual(run.compress(raw), raw)

    def test_empty_input(self):
        self.assertEqual(run.compress(""), "")

    def test_monotonic_tiny_table(self):
        # Single tiny finding — distilled may not beat raw bytes;
        # processor must passthrough.
        raw = (
            "┌──────┬──────┬──────┬──────┬──────┬──────┬──────┐\n"
            "│ Library │ Vulnerability │ Severity │ Status │ Installed Version │ Fixed Version │ Title │\n"
            "├──────┼──────┼──────┼──────┼──────┼──────┼──────┤\n"
            "│ x │ CVE-1-1 │ HIGH │ fixed │ 1.0 │ 1.1 │ Bad │\n"
            "└──────┴──────┴──────┴──────┴──────┴──────┴──────┘\n"
        )
        out = run.compress(raw)
        self.assertLessEqual(
            len(out.encode("utf-8")), len(raw.encode("utf-8"))
        )


class TestMultiLib(unittest.TestCase):
    def test_lib_carries_across_separator_with_blank_lib_cell(self):
        # Trivy continues using the lib name across rows by putting
        # a blank `│          │` for the Library column. The next-row
        # vuln cell is non-blank, so the prior finding flushes; the
        # new finding inherits the lib name.
        raw = (
            "┌──────────┬────────────────┬──────────┬────────┬───────────────────┬───────────────┬───────────┐\n"
            "│ Library  │ Vulnerability  │ Severity │ Status │ Installed Version │ Fixed Version │  Title    │\n"
            "├──────────┼────────────────┼──────────┼────────┼───────────────────┼───────────────┼───────────┤\n"
            "│ django   │ CVE-2019-14234 │ CRITICAL │ fixed  │ 2.2.1             │ 2.2.4         │ A title   │\n"
            "│          ├────────────────┤          │        │                   ├───────────────┼───────────┤\n"
            "│          │ CVE-2019-19844 │          │        │                   │ 2.2.9         │ Another   │\n"
            "└──────────┴────────────────┴──────────┴────────┴───────────────────┴───────────────┴───────────┘\n"
        )
        out = run.compress(raw)
        rows = _findings(out)
        self.assertEqual(len(rows), 2)
        # Both rows must show django@2.2.1 (carried forward)
        self.assertIn("django@2.2.1", rows[0])
        self.assertIn("django@2.2.1", rows[1])


if __name__ == "__main__":
    unittest.main(verbosity=2)
