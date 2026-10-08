---
type: Module
title: internal/pdfocr
description: 3 go files; 3 exported symbols.
attributes:
  - { name: exported, value: "3" }
  - { name: files, value: "3" }
  - { name: package, value: pdfocr }
edges:
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 1, source: internal/pdfocr/pdfocr.go }
---
# internal/pdfocr

<!-- signpost:managed:summary -->
3 go files; 3 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
3 files:
- `internal/pdfocr/pdfocr.go`
- `internal/pdfocr/pdfocr_test.go`
- `internal/pdfocr/render_test.go`

- **Exports** (3): `Options`, `PageCount`, `Transcribe`

- **Imports**: [internal/inferd](./inferd.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
