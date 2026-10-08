---
type: Module
title: cmd/thlibo/casecmd
description: 3 go files; 8 exported symbols.
attributes:
  - { name: exported, value: "8" }
  - { name: files, value: "3" }
  - { name: package, value: casecmd }
edges:
  - { kind: imports, to: ./casefile.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/case.go }
  - { kind: imports, to: ./compresscmd.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/case.go }
  - { kind: imports, to: ./inferd.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/ocr.go }
  - { kind: imports, to: ./install.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/ocr.go }
  - { kind: imports, to: ./logx.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/case.go }
  - { kind: imports, to: ./pdfocr.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/ocr.go }
  - { kind: imports, to: ./version.md, confidence: extracted, weight: 1, source: cmd/thlibo/casecmd/case.go }
---
# cmd/thlibo/casecmd

<!-- signpost:managed:summary -->
3 go files; 8 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
3 files:
- `cmd/thlibo/casecmd/case.go`
- `cmd/thlibo/casecmd/case_test.go`
- `cmd/thlibo/casecmd/ocr.go`

- **Exports** (8): `ExitLowValue`, `ExitNotRegular`, `ExitOK`, `ExitPruneFailed`, `ExitSourceMissing`, `ExitUsage`, `ExitWriteFailed`, `Run`

- **Imports**: [internal/casefile](./casefile.md) ×1, [cmd/thlibo/compresscmd](./compresscmd.md) ×1, [internal/inferd](./inferd.md) ×1, [internal/install](./install.md) ×1, [internal/logx](./logx.md) ×1, [internal/pdfocr](./pdfocr.md) ×1, [internal/version](./version.md) ×1
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
