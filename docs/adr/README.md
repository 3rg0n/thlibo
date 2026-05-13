# Architecture Decision Records

Cross-cutting architectural decisions are recorded here. Small
implementation choices aren't.

| # | Title | Status |
|---|---|---|
| [0001](0001-compression-via-pretooluse-rewrite.md) | Compression via PreToolUse rewrite, not proxy or PATH shim | Accepted |
| [0002](0002-one-warm-model-single-daemon.md) | One warm model, single daemon | Accepted |
| [0003](0003-per-user-autostart-not-system-service.md) | Per-user autostart, not a system service | Accepted |

## Writing a new ADR

See global guidance in `~/.claude/CLAUDE.md` or the existing ADRs
for the format. Short version:

- One page. Nygard short form.
- Filename `NNNN-kebab-case-title.md`, zero-padded sequential.
- Once accepted, ADRs are immutable. To revise, write a new ADR
  and set the old one's status to `superseded by NNNN`.
- Reference the ADR from the corresponding `CHANGELOG.md` entry.
