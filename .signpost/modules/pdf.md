---
type: Module
title: internal/pdf
description: 26 go files; 110 exported symbols.
attributes:
  - { name: exported, value: "110" }
  - { name: files, value: "26" }
  - { name: package, value: pdf }
edges:
  - { kind: imports, to: ../references/go-golang-org-x-image.md, confidence: extracted, weight: 2, source: internal/pdf/filters.go }
---
# internal/pdf

<!-- signpost:managed:summary -->
26 go files; 110 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
26 files:
- `internal/pdf/bounds_test.go`
- `internal/pdf/decompress_test.go`
- `internal/pdf/document.go`
- `internal/pdf/filters.go`
- `internal/pdf/filters_test.go`
- `internal/pdf/fuzz_test.go`
- `internal/pdf/glyphlist.go`
- `internal/pdf/header_offset_test.go`
- `internal/pdf/helper_test.go`
- `internal/pdf/images.go`
- `internal/pdf/images_test.go`
- `internal/pdf/lexer.go`
- `internal/pdf/lexer_test.go`
- `internal/pdf/meta.go`
- `internal/pdf/objects.go`
- `internal/pdf/parser.go`
- `internal/pdf/reader.go`
- `internal/pdf/samples.go`
- `internal/pdf/samples_test.go`
- `internal/pdf/security.go`
- `internal/pdf/stdfonts.go`
- `internal/pdf/structure.go`
- `internal/pdf/structure_test.go`
- `internal/pdf/table.go`
- `internal/pdf/table_test.go`
- `internal/pdf/text.go`

- **Exports** (110): `Array`, `BuildLines`, `Cell`, `Column`, `DecodeImageStream`, `DecodeTextString`, `Dict`, `Dict.Array`, `Dict.Dict`, `Dict.Float`, `Dict.Int`, `Dict.Name`, `Dict.Ref`, `Dict.Stream`, `Dict.String`, `Document`, `Document.InfoString`, `Document.IsEncrypted`, `Document.IsTagged`, `Document.NumPages`, `Document.Outline`, `Document.Page`, `Document.StructuredMarkdown`, `Document.Text`, `ErrEncrypted`, `ErrUnsupportedFilter`, `ExtractPageImages`, `ExtractPageText`, `ExtractText`, `ExtractTextWithResources`, `FindTable`, `FindTableAcrossPages`, `FindTables`, `HelveticaTextWidth`, `ImageRef`, `ImageRef.Bounds`, `ImageRef.CoversPage`, `ImageRef.Decode`, `ImageRef.PixelSize`, `Lexer`, `Lexer.AtEnd`, `Lexer.NextToken`, `Lexer.Pos`, `Lexer.SetPos`, `Name`, `NewLexer`, `NewParser`, `Open`, `OpenBytes`, `OutlineItem`, `Page`, `Page.FindTable`, `Page.HasImages`, `Page.Images`, `Page.MediaBox`, `Page.Rotation`, `Page.ScanImage`, `Page.Tables`, `Page.Text`, `Page.TextLines`, and 50 more

- **Imports**: [golang.org/x/image](../references/go-golang-org-x-image.md) ×2
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
