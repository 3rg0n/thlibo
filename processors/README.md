# Built-in processors

These ship embedded in the `thlibo` binary via `go:embed` (see
`embed.go` in this directory) and are also copied to
`~/.thlibo/processors/` at install time (`thlibo install`, Phase 6).
That means:

- Fresh binary with no install: built-ins are still available because
  the registry uses the embedded FS as its builtin source (gate C4).
- After install: the same files exist on disk, so users can edit
  them in place. Edits shadow the embedded versions (gate C5).

Current contents:

| Name | Type | Entry |
|---|---|---|
| `compress` | prompt | `processor.md` |
| `casefolder` | prompt | `processor.md` (thinking-enabled) |
| `git-filter` | script | `processor.yaml` + `run.py` |
| `npm-filter` | script | `processor.yaml` + `run.py` |
| `cargo-filter` | script | `processor.yaml` + `run.py` |

A user processor in `~/.thlibo/processors/<name>/` with the same name
overrides the built-in, regardless of whether the install-time copy
still exists on disk.
