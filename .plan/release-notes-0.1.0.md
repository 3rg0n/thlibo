# thlibo v0.1.0 release notes

## Token savings measurements (F6)

These numbers come from `internal/middleware.TestTokenSavingsTable`
run on 2026-05-13 against the embedded built-in script processors.
Each row represents real output from the kind of tool the processor
targets, piped through the same `middleware.Pipeline` a real
`thlibo exec` invocation uses.

Prompt processors (`compress`, `casefolder`) are not in this table
because their output depends on a live Gemma 4 daemon; their
per-release numbers come from the release-machine smoke test.

| processor     | fixture                          | raw bytes | compressed | reduction |
|---------------|----------------------------------|----------:|-----------:|----------:|
| git-filter    | `git status` (50-file tree)      |     3,444 |      3,259 |     5.4%  |
| git-filter    | `git diff HEAD~N` (50 files)     |    52,260 |      1,240 |    97.6%  |
| npm-filter    | `npm list` (~200 deps)           |     9,228 |         52 |    99.4%  |
| cargo-filter  | `cargo test` (120 tests, 20 fail)|    13,227 |      1,434 |    89.2%  |

### Interpretation

- **git diff, npm list, cargo test — where the real wins live.** Diff
  hunks collapse to one-line `diff file (+N -M)` summaries; the npm
  dependency tree is reduced to counts + per-package lines; cargo test
  keeps just the failing tests and the final result line.

- **git status is the outlier.** On a tree with many modified files
  but no diffs or logs, the only compression available is hint-line
  and blank-separator stripping — ~5%. Real-world `git status` output
  this large is rare; most status calls fall below thlibo's 2000-byte
  short-circuit and pass through unchanged.

### How to reproduce

```
go test ./internal/middleware/... -run TestTokenSavingsTable -v
```

The test logs the markdown table to the test output and writes it to
a file under the test's temp dir. Numbers are reproducible across
Go versions; the Python script processors pass stdin through
`python3` whose version shouldn't affect output bytes.

### Live-daemon measurement (compression of unknown output)

One end-to-end measurement with a running `thlibod` + stub engine
on Windows on 2026-05-13:

- `git diff HEAD~5` on the thlibo repo itself: **65,209 → 954 bytes (98.5% reduction).**

Measured via `thlibo exec -- git diff HEAD~5 | wc -c` with the daemon
bound to `\\.\pipe\thlibo-infer`. Fast-path match on git-filter
dispatches the Python script directly; the daemon is not involved in
this particular measurement, but the wire path (hook → `thlibo
exec` → middleware → script dispatcher → compressed stdout) is.

### Full Claude Code round-trip (2026-05-13)

Using `claude -p` with the thlibo PreToolUse hook installed in
`~/.claude/settings.json`:

- Prompt: *"Use the Bash tool to run exactly: git diff HEAD~5. Then
  show me verbatim the first 300 characters of stdout you received."*
- Claude Code debug log (excerpt): hook `success`,
  `updatedInput.command = C:/dev/Github/thlibo/.test/bin/thlibo.exe
  exec -- git diff HEAD~5`, `permissionDecision: allow`.
- `thlibo-exec.ndjson`:
  `{"msg":"done","raw_bytes":94806,"out_bytes":778,"reduction_pct":99.18,...}`.
- Model's verbatim response first line:
  `diff .github/workflows/release.yml (+43 -4)` — git-filter's
  compressed per-file summary format. The model never saw the raw
  diff hunks.

This closes the last-mile uncertainty: hook fires → rewrite takes
effect → subprocess runs → middleware compresses → compressed bytes
land in the model's context. End-to-end observable, reproducible,
and correct.

An aside: asking the model to *self-report byte counts* of its tool
outputs is unreliable — in an earlier probe it claimed to have
received 94,806 bytes (the raw diff size) while the log shows it
actually received 778. The model pattern-matches plausible-looking
numbers rather than counting. Ask for verbatim content to verify
what was actually delivered.

### Prompt processors

Token savings for `compress` and `casefolder` will be measured at
tag time against a real llamafile + Gemma 4 E4B. v0.1.0 ships the
pinned infrastructure (GGUF SHA-256, hook transport, compression
pipeline) proven via script processors.
