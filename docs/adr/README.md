# Architecture Decision Records

Cross-cutting architectural decisions are recorded here. Small
implementation choices aren't.

| # | Title | Status |
|---|---|---|
| [0001](0001-compression-via-pretooluse-rewrite.md) | Compression via PreToolUse rewrite, not proxy or PATH shim | Accepted |
| [0002](0002-one-warm-model-single-daemon.md) | One warm model, single daemon | Superseded by [0005](0005-extract-inference-to-inferd.md) |
| [0003](0003-per-user-autostart-not-system-service.md) | Per-user autostart, not a system service | Accepted |
| [0004](0004-no-windows-shim.md) | No Windows shim binary — plain PATH installation | Accepted |
| [0005](0005-extract-inference-to-inferd.md) | Extract inference to a separate `inferd` service | Accepted |
| [0006](0006-fail-open-during-inferd-bootstrap.md) | Fail open during the inferd bootstrap window | Accepted |
| [0007](0007-pdf-to-markdown.md) | PDF → Markdown converter (pypdf + pdfplumber, Python script processor) | Accepted (scanned-PDF deferral superseded by [0009](0009-pdf-image-ocr-via-gemma-vision.md)) |
| [0008](0008-numpy-as-processor-dep.md) | numpy as a processor dep, soft-imported with passthrough fallback | Accepted |
| [0009](0009-pdf-image-ocr-via-gemma-vision.md) | Scanned-PDF OCR via Gemma vision, dispatched Go-side | Accepted |
| [0010](0010-native-go-processors.md) | Reimplement the 9 deterministic built-in processors as native Go (`pdf-to-md` / `cordon-filter` stay Python) | Accepted |
| [0011](0011-optional-otel-emission.md) | Optional OpenTelemetry emission (metrics + events), opt-in and content-free | Accepted |

## Writing a new ADR

See global guidance in `~/.claude/CLAUDE.md` or the existing ADRs
for the format. Short version:

- One page. Nygard short form.
- Filename `NNNN-kebab-case-title.md`, zero-padded sequential.
- Once accepted, ADRs are immutable. To revise, write a new ADR
  and set the old one's status to `superseded by NNNN`.
- Reference the ADR from the corresponding `CHANGELOG.md` entry.
