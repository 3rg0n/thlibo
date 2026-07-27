#!/usr/bin/env python3
"""Unit tests for pdf-to-md layout-grid rejection and furniture stripping.

Run: python -m unittest processors/pdf-to-md/testdata/test_layout.py

The slide-deck regression: a 35-slide deck extracted to 125 KB of
markdown from a 55 KB text layer, because pdfplumber reported every
invisible layout grid as a table (47 "tables", 130 structurally empty
rows) and every slide's nav sidebar + "Cisco Confidential" footer was
emitted verbatim on all 35 pages. These tests pin the two filters so
that doesn't recur.

Thresholds in run.py were derived from that deck: real tables had
>=2 columns, <=212-char cells and >=0.46 fill density; layout grids
were single-column or >=1678-char cells at 0.11-0.30 density.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

# Add the pdf-to-md directory to sys.path so we can import run.py
ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

import importlib.util  # noqa: E402

spec = importlib.util.spec_from_file_location("pdf_run", ROOT / "run.py")
pdf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pdf)


class TestIsLayoutGrid(unittest.TestCase):
    def test_real_data_table_kept(self):
        """The shape of the deck's page-10 criteria table: 3 columns,
        short cells, mostly filled."""
        table = [
            ["Collects personal data (PII)", "User credentials", "Email, CEC ID"],
            [None, "Employee name", "John Doe, Jan Kowalski"],
            [None, "Contact information", "Email, phone, address"],
        ]
        self.assertFalse(pdf.is_layout_grid(table))

    def test_prose_crammed_cell_rejected(self):
        """A layout grid dumps a whole slide's prose into one cell.
        Real tables topped out at 212 chars."""
        table = [["short", "x" * 1700], ["a", "b"]]
        self.assertTrue(pdf.is_layout_grid(table))

    def test_sparse_nav_sidebar_rejected(self):
        """The repeated nav sidebar: 72 cells, 8 filled (density 0.11)."""
        table = [[None] * 8 for _ in range(9)]
        for r in range(8):
            table[r][0] = f"Section {r}"
        self.assertTrue(pdf.is_layout_grid(table))

    def test_single_column_rejected(self):
        """One column carries no row/column relationship — it conveys
        nothing a paragraph doesn't. The deck's leftover artifacts were
        stacked TOC panels of exactly this shape."""
        table = [["Introduction to Works Councils"], ["Section summary"]]
        self.assertTrue(pdf.is_layout_grid(table))

    def test_small_dense_table_kept(self):
        """A 2x2 fully-filled table is legitimate and must survive the
        density test."""
        table = [["CEC ID", "Last login"], ["abc123", "2026-01-01"]]
        self.assertFalse(pdf.is_layout_grid(table))

    def test_long_descriptive_cell_kept(self):
        """A real table may carry one long descriptive column. The
        threshold is deliberately loose here: dropping a real table
        loses data, whereas keeping a layout grid only costs bytes."""
        table = [["Requirement", "x" * 900], ["another", "y" * 500]]
        self.assertFalse(pdf.is_layout_grid(table))

    def test_cell_length_boundary(self):
        """Pin the threshold itself so a constant change can't slip
        through: at the cap it survives, one over it doesn't."""
        cap = pdf._MAX_TABLE_CELL_CHARS
        self.assertFalse(pdf.is_layout_grid([["a", "x" * cap], ["b", "c"]]))
        self.assertTrue(pdf.is_layout_grid([["a", "x" * (cap + 1)], ["b", "c"]]))

    def test_density_boundary(self):
        """10 cells, 4 filled = 0.40 exactly -> kept (>= threshold);
        3 filled = 0.30 -> rejected."""
        self.assertEqual(pdf._MIN_TABLE_DENSITY, 0.40)
        at = [["a", "b", "c", "d", None], [None] * 5]
        self.assertFalse(pdf.is_layout_grid(at))
        below = [["a", "b", "c", None, None], [None] * 5]
        self.assertTrue(pdf.is_layout_grid(below))

    def test_degenerate_input(self):
        self.assertTrue(pdf.is_layout_grid([]))
        self.assertTrue(pdf.is_layout_grid([[]]))
        self.assertTrue(pdf.is_layout_grid([[None, None], [None, None]]))


class TestFindFurniture(unittest.TestCase):
    def test_running_footer_detected(self):
        pages = [f"Cisco Confidential\nSlide {i} body text" for i in range(10)]
        self.assertIn("Cisco Confidential", pdf.find_furniture(pages))

    def test_content_line_not_furniture(self):
        """A line on a minority of pages is content. The deck's most
        repeated genuine line ran 4/35 pages."""
        pages = ["unique body " + str(i) for i in range(10)]
        pages[0] += "\nshared note"
        pages[1] += "\nshared note"
        self.assertNotIn("shared note", pdf.find_furniture(pages))

    def test_short_document_exempt(self):
        """On a 3-page document "60% of pages" is two pages, which is
        not a pattern worth acting on."""
        pages = ["Header\nbody one", "Header\nbody two", "Header\nbody three"]
        self.assertEqual(pdf.find_furniture(pages), set())

    def test_long_line_never_furniture(self):
        """A repeated paragraph is boilerplate the reader may still
        want; only short lines are chrome."""
        para = "x" * 200
        pages = [f"{para}\nbody {i}" for i in range(10)]
        self.assertNotIn(para, pdf.find_furniture(pages))

    def test_page_fraction_boundary(self):
        """Exactly at the threshold counts as furniture: 10 pages, a
        line on 6 of them is 0.60 >= 0.60. On 5 it is not."""
        at = [f"Header\nbody {i}" for i in range(6)] + [f"body {i}" for i in range(4)]
        self.assertIn("Header", pdf.find_furniture(at))
        below = [f"Header\nbody {i}" for i in range(5)] + [f"body {i}" for i in range(5)]
        self.assertNotIn("Header", pdf.find_furniture(below))

    def test_min_pages_boundary(self):
        """4 pages is the floor -> furniture detection active; 3 is not."""
        four = ["Header\nbody %d" % i for i in range(4)]
        self.assertIn("Header", pdf.find_furniture(four))
        self.assertEqual(pdf.find_furniture(four[:3]), set())

    def test_repeat_within_one_page_does_not_qualify(self):
        """Counting per-page (not per-occurrence) stops one busy page
        from nominating its own text as document furniture."""
        pages = ["dup\ndup\ndup\ndup\ndup"] + [f"body {i}" for i in range(9)]
        self.assertNotIn("dup", pdf.find_furniture(pages))


class TestStripFurniture(unittest.TestCase):
    def test_removes_only_furniture(self):
        text = "Cisco Confidential\nReal content here\nTable of Contents"
        got = pdf.strip_furniture(text, {"Cisco Confidential", "Table of Contents"})
        self.assertEqual(got, "Real content here")

    def test_collapses_resulting_blank_runs(self):
        text = "keep\nchrome\n\nchrome\n\nalso keep"
        got = pdf.strip_furniture(text, {"chrome"})
        self.assertEqual(got, "keep\n\nalso keep")

    def test_empty_furniture_set_is_identity(self):
        text = "line one\nline two"
        self.assertEqual(pdf.strip_furniture(text, set()), text)

    def test_all_furniture_yields_empty(self):
        """main() falls back to the original text when this happens, so
        a furniture-only page still renders."""
        self.assertEqual(pdf.strip_furniture("chrome\nchrome", {"chrome"}), "")


if __name__ == "__main__":
    unittest.main()
