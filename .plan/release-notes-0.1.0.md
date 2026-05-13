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

Prompt-processor token savings (compress, casefolder) will be
measured against a real llamafile + Gemma 4 E4B at first public
release. v0.1.0 ships pinned infrastructure; the GGUF SHA-256 and
prompt-processor numbers get filled in at tag time.
