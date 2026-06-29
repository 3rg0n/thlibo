#!/usr/bin/env python3
"""go-test-filter: compress `go test -v` stdout for an AI assistant.

`go test -v` interleaves a `=== RUN` / `--- PASS` line pair for every
passing test with the failures and the final package tally. On a large
suite the passing-test noise dwarfs the few lines an AI actually needs.

Kept verbatim (load-bearing):
  - Build / compile errors: a `# pkg` header followed by
    `file.go:line:col: msg` lines (and `FAIL pkg [build failed]`).
  - Every failing test: its `--- FAIL: Name` line plus the indented
    output beneath it (t.Errorf/Fatalf messages, file:line, got/want),
    up to the next test boundary.
  - Panics: a `panic:` line and its goroutine stack, verbatim.
  - The per-package result lines (`ok pkg`, `FAIL pkg`, `? pkg [no test
    files]`) and the final bare `PASS` / `FAIL`.

Dropped:
  - `=== RUN` / `=== PAUSE` / `=== CONT` lines.
  - `--- PASS:` lines and their indented output (passing tests).
  - `--- SKIP:` collapses to a one-line count (names dropped).
  - ANSI colour codes; repeated blank lines.

Two input shapes are handled: plain `-v` text and `-json` (one JSON
event per line, `go test -json`). JSON is decoded to the same summary.

Monotonic guarantee: the distilled output is emitted only when it is
strictly smaller (UTF-8 bytes) than the input; otherwise the input is
returned unchanged. Anything that doesn't look like `go test` output, or
any unexpected exception, passes through verbatim.
"""

from __future__ import annotations

import json
import re
import sys

# Force UTF-8 + LF: Windows defaults stdout to cp1252 (crashes on
# non-ANSI test output) and CRLF (breaks byte-identity). Same fix the
# other processors use.
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", newline="")

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

RUN_RE = re.compile(r"^\s*=== (?:RUN|PAUSE|CONT|NAME)\s")
PASS_RE = re.compile(r"^\s*--- PASS:\s")
FAIL_RE = re.compile(r"^\s*--- FAIL:\s+(?P<name>\S+)")
SKIP_RE = re.compile(r"^\s*--- SKIP:\s")
# Package tally lines.
PKG_OK_RE = re.compile(r"^ok\s+\S+")
PKG_FAIL_RE = re.compile(r"^FAIL\s+\S+")
PKG_NOTEST_RE = re.compile(r"^\?\s+\S+\s+\[no test files\]")
BARE_RESULT_RE = re.compile(r"^(?:PASS|FAIL)\s*$")
# Build-error header: `# package/path` (optionally `[pkg.test]`).
BUILD_HDR_RE = re.compile(r"^# \S")
BUILD_FAIL_RE = re.compile(r"^FAIL\s+\S+\s+\[build failed\]")
PANIC_RE = re.compile(r"^(panic:|fatal error:)\s")
# A line that starts a new test result block (used to bound failure
# output capture).
BLOCK_BOUNDARY_RE = re.compile(r"^\s*(?:=== |--- (?:PASS|FAIL|SKIP):)")


def looks_like_go_test(text: str) -> bool:
    # Heuristic: any of the characteristic markers. Keep it cheap.
    return bool(
        re.search(r"^\s*=== RUN\s", text, re.M)
        or re.search(r"^\s*--- (?:PASS|FAIL|SKIP):", text, re.M)
        or re.search(r"^(?:ok|FAIL|\?)\s+\S+", text, re.M)
        or re.search(r"^# \S+\n.+\.go:\d+:", text, re.M)
        # `go test -json`: events carry "Action" + a Go test "Test"/"Package".
        or re.search(r'^\{"Time":|^\{"Action":"(?:run|output|pass|fail|skip)"', text, re.M)
    )


def _compress_json(lines: list[str]) -> "list[str] | None":
    """Distil `go test -json` events. Returns output lines, or None if
    the input isn't JSON-event-shaped (caller falls back to text path)."""
    out: list[str] = []
    failed_output: dict[str, list[str]] = {}
    fail_order: list[str] = []
    pkg_results: list[str] = []
    saw_json = False
    skipped = 0
    for ln in lines:
        s = ln.strip()
        if not s:
            continue
        try:
            ev = json.loads(s)
        except (ValueError, TypeError):
            return None  # not JSON — let the text path handle it
        if not isinstance(ev, dict) or "Action" not in ev:
            return None
        saw_json = True
        action = ev.get("Action")
        test = ev.get("Test")
        pkg = ev.get("Package", "")
        out_txt = ev.get("Output", "")
        if action == "output" and test:
            # Buffer per-test output; we only emit it if the test fails.
            failed_output.setdefault(test, []).append(out_txt.rstrip("\n"))
        elif action == "fail" and test:
            if test not in fail_order:
                fail_order.append(test)
        elif action == "skip" and test:
            skipped += 1
        elif action in ("pass", "fail") and not test:
            # package-level result
            verb = "ok" if action == "pass" else "FAIL"
            pkg_results.append(f"{verb} {pkg}")
    if not saw_json:
        return None
    for name in fail_order:
        out.append(f"--- FAIL: {name}")
        for o in failed_output.get(name, []):
            t = o.strip()
            # keep only the substantive failure lines (drop the
            # === RUN / --- FAIL echoes go -json duplicates)
            if t and not BLOCK_BOUNDARY_RE.match(o):
                out.append("    " + t)
    if skipped:
        out.append(f"(skipped {skipped} test(s))")
    out.extend(pkg_results)
    return out


def _compress_text(lines: list[str]) -> list[str]:
    out: list[str] = []
    # buf holds the output lines emitted *after* the current test's
    # `=== RUN` and *before* its terminal `--- PASS/FAIL/SKIP` — in
    # `go test -v` the failure detail (t.Errorf/file:line) prints here.
    # We emit buf only when the test FAILs; discard it on PASS.
    buf: list[str] = []
    in_test = False
    skipped = 0
    i = 0
    n = len(lines)
    while i < n:
        raw = ANSI_RE.sub("", lines[i]).rstrip()

        # Build-error header → keep it + following lines verbatim until a
        # blank or package-result line.
        if BUILD_HDR_RE.match(raw):
            out.append(raw)
            i += 1
            while i < n:
                nxt = ANSI_RE.sub("", lines[i]).rstrip()
                if nxt == "" or PKG_FAIL_RE.match(nxt) or BUILD_FAIL_RE.match(nxt):
                    break
                out.append(nxt)
                i += 1
            continue

        # Panic → keep the panic line + stack until a package result.
        if PANIC_RE.match(raw):
            out.append(raw)
            i += 1
            while i < n:
                nxt = ANSI_RE.sub("", lines[i]).rstrip()
                if PKG_FAIL_RE.match(nxt) or PKG_OK_RE.match(nxt):
                    break
                out.append(nxt)
                i += 1
            continue

        # Start of a test → reset the per-test buffer.
        if RUN_RE.match(raw):
            in_test = True
            buf = []
            i += 1
            continue

        # Terminal markers: emit (FAIL) or discard (PASS/SKIP) the buffer.
        if FAIL_RE.match(raw):
            out.extend(buf)
            out.append(raw)  # `--- FAIL: Name (0.00s)`
            buf = []
            in_test = False
            i += 1
            continue
        if PASS_RE.match(raw):
            buf = []
            in_test = False
            i += 1
            continue
        if SKIP_RE.match(raw):
            skipped += 1
            buf = []
            in_test = False
            i += 1
            continue

        # Package-result + bare-result lines: keep; flush any stray buffer.
        if PKG_OK_RE.match(raw) or PKG_FAIL_RE.match(raw) or PKG_NOTEST_RE.match(raw) or BUILD_FAIL_RE.match(raw):
            out.extend(buf)
            buf = []
            in_test = False
            out.append(raw)
            i += 1
            continue
        if raw == "PASS":
            buf = []  # bare PASS sentinel — drop
            i += 1
            continue
        if BARE_RESULT_RE.match(raw):  # bare FAIL sentinel — keep
            out.append(raw)
            i += 1
            continue

        # Any other line: inside a test it's buffered (emitted only if the
        # test fails); outside a test it's kept (log output, headers).
        if raw == "":
            i += 1
            continue
        if in_test:
            buf.append(raw)
        else:
            out.append(raw)
        i += 1

    out.extend(buf)  # trailing buffer (no terminal marker seen)
    if skipped:
        out.append(f"(skipped {skipped} test(s))")
    return out


def compress(text: str) -> str:
    if not looks_like_go_test(text):
        return text
    lines = text.splitlines()
    result = _compress_json(lines)
    if result is None:
        result = _compress_text(lines)
    distilled = "\n".join(result)
    if distilled and not distilled.endswith("\n"):
        distilled += "\n"
    # Monotonic guarantee: only return the distillation if it's a strict
    # byte win; otherwise the original is what the AI gets.
    if len(distilled.encode("utf-8")) < len(text.encode("utf-8")):
        return distilled
    return text


def main() -> int:
    try:
        data = sys.stdin.buffer.read().decode("utf-8", errors="replace")
    except Exception:  # noqa: BLE001
        return 0
    try:
        sys.stdout.write(compress(data))
    except Exception:  # noqa: BLE001 — never break the client; emit original
        sys.stdout.write(data)
    return 0


if __name__ == "__main__":
    sys.exit(main())
