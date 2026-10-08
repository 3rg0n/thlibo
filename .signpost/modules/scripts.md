---
type: Module
title: scripts
description: "3 powershell files; 10 exported symbols; entrypoint #!, __main__; package scripts.run_processor_tests."
tags: [entrypoint]
attributes:
  - { name: entrypoints, value: "#!, __main__" }
  - { name: exported, value: "10" }
  - { name: files, value: "3" }
  - { name: package, value: scripts.run_processor_tests }
---
# scripts

<!-- signpost:managed:summary -->
3 powershell files; 10 exported symbols; entrypoint #!, __main__; package scripts.run_processor_tests.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
3 files:
- `scripts/install.ps1`
- `scripts/install.sh`
- `scripts/run_processor_tests.py`

- **Exports** (10): `Die`, `ROOT`, `Resolve-Tag`, `Say`, `detect_platform`, `die`, `main`, `require`, `resolve_tag`, `say`
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
