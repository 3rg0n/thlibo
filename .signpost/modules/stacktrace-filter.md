---
type: Module
title: processors/stacktrace-filter
description: 1 python file; 23 exported symbols; entrypoint __main__; package processors.stacktrace-filter.run.
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: __main__ }
  - { name: exported, value: "23" }
  - { name: files, value: "1" }
  - { name: package, value: processors.stacktrace-filter.run }
---
# processors/stacktrace-filter

<!-- signpost:managed:summary -->
1 python file; 23 exported symbols; entrypoint __main__; package processors.stacktrace-filter.run.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
1 file:
- `processors/stacktrace-filter/run.py`

- **Exports** (23): `ANSI_RE`, `GO_FRAME_LOC_RE`, `GO_FRAME_RE`, `GO_GOROUTINE_RE`, `GO_PANIC_RE`, `JAVA_AT_RE`, `JAVA_CAUSED_BY_RE`, `JAVA_EXC_RE`, `KEEP_HEAD`, `KEEP_TAIL`, `NODE_ANON_FRAME_RE`, `NODE_FRAME_RE`, `PY_EXC_RE`, `PY_FRAME_RE`, `PY_START_RE`, `RUST_FRAME_LOC_RE`, `RUST_FRAME_RE`, `RUST_PANIC_RE`, `compress`, `compress_block`, `main`, `split_traces`, `strip_ansi`
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
