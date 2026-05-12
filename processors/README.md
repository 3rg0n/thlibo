# Built-in processors

These ship embedded in the `thlibo` binary via `go:embed`. Populated in Phase 4.

Expected subdirectories per spec:

- `compress/` — prompt processor (`processor.md`)
- `casefolder/` — prompt processor (`processor.md`)
- `git-filter/` — script processor (`processor.yaml` + `run.py`)
- `npm-filter/` — script processor (`processor.yaml` + `run.py`)
- `cargo-filter/` — script processor (`processor.yaml` + `run.py`)

A user processor in `~/.thlibo/processors/<name>/` with the same name
overrides the built-in.
