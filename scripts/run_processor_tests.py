#!/usr/bin/env python3
"""Run each script processor's unittest file in its own process.

Used by CI (`.github/workflows/ci.yml`) and runnable locally:

    python scripts/run_processor_tests.py

Why a script rather than a shell loop: CI runs this job as a matrix
across ubuntu / macos / windows, and the Windows runner's default shell
is pwsh, where a bash `while ... done` loop is a parse error.

Why one process per file: several of these tests import their processor
with a bare `import run`, so a single batched `unittest` run lets the
first processor's run.py win the `run` module name for every subsequent
test. Separate processes keep each test importing its own.

Exits non-zero if any file fails, naming the failures.
"""

from __future__ import annotations

import subprocess  # nosec B404 - fixed argv, no shell, paths from rglob
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def main() -> int:
    tests = sorted((ROOT / "processors").rglob("test_*.py"))
    if not tests:
        print("no processor tests found", file=sys.stderr)
        return 1

    failed: list[str] = []
    for t in tests:
        rel = t.relative_to(ROOT).as_posix()
        print(f"== {rel}", flush=True)
        proc = subprocess.run(  # noqa: S603  # nosec B603 - fixed argv, no shell
            [sys.executable, "-m", "unittest", str(t)],
            cwd=ROOT,
        )
        if proc.returncode != 0:
            failed.append(rel)

    if failed:
        print(f"\nFAILED: {', '.join(failed)}", file=sys.stderr)
        return 1
    print(f"\nall {len(tests)} processor test files passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
