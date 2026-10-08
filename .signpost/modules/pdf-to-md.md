---
type: Module
title: processors/pdf-to-md
description: 1 python file; 11 exported symbols; entrypoint __main__; package processors.pdf-to-md.run.
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: __main__ }
  - { name: exported, value: "11" }
  - { name: files, value: "1" }
  - { name: package, value: processors.pdf-to-md.run }
edges:
  - { kind: configures, to: ../references/pypi-pdfplumber.md, confidence: extracted, source: processors/pdf-to-md/requirements.txt }
  - { kind: imports, to: ../references/pypi-pdfplumber.md, confidence: extracted, weight: 1, source: processors/pdf-to-md/run.py }
  - { kind: configures, to: ../references/pypi-pypdf.md, confidence: extracted, source: processors/pdf-to-md/requirements.txt }
  - { kind: imports, to: ../references/pypi-pypdf.md, confidence: extracted, weight: 1, source: processors/pdf-to-md/run.py }
---
# processors/pdf-to-md

<!-- signpost:managed:summary -->
1 python file; 11 exported symbols; entrypoint __main__; package processors.pdf-to-md.run.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `processors/pdf-to-md/run.py`

- **Exports** (11): `emit_error`, `find_furniture`, `is_layout_grid`, `main`, `page_count`, `page_strategy`, `promote_headings`, `render_outline`, `render_page`, `render_table`, `strip_furniture`

- **Configures**: [pdfplumber](../references/pypi-pdfplumber.md), [pypdf](../references/pypi-pypdf.md)

- **Imports**: [pdfplumber](../references/pypi-pdfplumber.md) ×1, [pypdf](../references/pypi-pypdf.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
