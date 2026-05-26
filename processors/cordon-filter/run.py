#!/usr/bin/env python3
"""cordon-filter: surface semantically rare windows in a log stream.

Reads stdin line-by-line, builds overlapping windows of WINDOW_SIZE
records, embeds each window via inferd's embed socket, then scores
windows by k-NN distance in embedding space (mean distance to the
K nearest neighbours). The TOP_PERCENTILE windows by score are
emitted as the same structured shape `compress` produces, so
downstream consumers don't need a second parser.

Reference: github.com/calebevans/cordon (Apache-2.0). Cordon is the
algorithmic blueprint; this is a clean reimplementation against
inferd's embedding endpoint, with no torch / no sentence-transformers
dependency.

Fallback contract (filter never breaks the AI client):
  - inferd embed socket unreachable / returns error  → input verbatim
  - numpy not installed                              → input verbatim
  - input < 2 * WINDOW_SIZE lines                    → input verbatim
  - any unhandled exception                          → input verbatim
"""

from __future__ import annotations

import json
import os
import re
import socket
import sys
from typing import Iterable, List, Optional, Tuple

WINDOW_SIZE = int(os.environ.get("CORDON_WINDOW_SIZE", "10"))
WINDOW_STRIDE = int(os.environ.get("CORDON_WINDOW_STRIDE", "5"))
K_NEIGHBOURS = int(os.environ.get("CORDON_K", "5"))
TOP_PERCENTILE = float(os.environ.get("CORDON_TOP_PERCENTILE", "20"))
EMBED_DIMENSIONS = int(os.environ.get("CORDON_EMBED_DIMENSIONS", "256"))
DIAL_TIMEOUT_S = float(os.environ.get("CORDON_DIAL_TIMEOUT", "5.0"))
EMBED_TIMEOUT_S = float(os.environ.get("CORDON_EMBED_TIMEOUT", "60.0"))
BATCH_SIZE = int(os.environ.get("CORDON_BATCH_SIZE", "32"))
MAX_WINDOWS = int(os.environ.get("CORDON_MAX_WINDOWS", "0"))  # 0 = no cap
# Per-window character cap. inferd ≥0.2.4 rejects oversized embed inputs
# at the wire layer cleanly (3rg0n/inferd#20), so this cap is no longer
# strictly required for safety. We keep a generous 4000-char default to
# avoid sending degenerate huge windows that won't embed well anyway —
# set CORDON_MAX_CHARS=0 to disable.
MAX_CHARS = int(os.environ.get("CORDON_MAX_CHARS", "4000"))

# Preserve LF on Windows (matches every other thlibo script processor).
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(newline="")


def _embed_socket_path() -> str:
    r"""Resolve inferd's embed socket path per ADR 0017.

    Linux:   ${XDG_RUNTIME_DIR}/inferd/infer.embed.sock
             → $HOME/.inferd/run/infer.embed.sock if XDG unset
    macOS:   ${TMPDIR}/inferd/infer.embed.sock
    Windows: \\.\pipe\inferd-infer-embed
    """
    if sys.platform == "win32":
        return r"\\.\pipe\inferd-infer-embed"
    if sys.platform == "darwin":
        # /tmp fallback matches inferd's published socket path (ADR 0017).
        return os.path.join(
            os.environ.get("TMPDIR", "/tmp"), "inferd", "infer.embed.sock"  # nosec B108
        )
    xdg = os.environ.get("XDG_RUNTIME_DIR")
    if xdg:
        return os.path.join(xdg, "inferd", "infer.embed.sock")
    home = os.path.expanduser("~")
    return os.path.join(home, ".inferd", "run", "infer.embed.sock")


def _windows(lines: List[str]) -> List[Tuple[int, str]]:
    """Slice the input into overlapping windows.

    Returns (start_line_index, joined_text) tuples. Stride < window
    size gives overlap, which makes anomalies more likely to land
    cleanly inside at least one window without straddling.
    """
    out: List[Tuple[int, str]] = []
    n = len(lines)
    if n < WINDOW_SIZE:
        return out
    start = 0
    while start + WINDOW_SIZE <= n:
        text = "\n".join(lines[start : start + WINDOW_SIZE])
        if MAX_CHARS > 0 and len(text) > MAX_CHARS:
            text = text[:MAX_CHARS]
        out.append((start, text))
        start += WINDOW_STRIDE
    return out


def _embed_batch(sock_path: str, inputs: List[str]) -> Optional[List[List[float]]]:
    """Send a single embed request to inferd and return the vectors.

    On any failure (connect, parse, error frame), returns None and the
    caller falls back to passthrough.
    """
    if sys.platform == "win32":
        try:
            with open(sock_path, "r+b", buffering=0) as pipe:
                return _embed_io(pipe, inputs)
        except OSError:
            return None
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(DIAL_TIMEOUT_S)
        s.connect(sock_path)
        s.settimeout(EMBED_TIMEOUT_S)
    except (OSError, socket.error):
        return None
    try:
        return _embed_io(s.makefile("rwb", buffering=0), inputs)
    finally:
        try:
            s.close()
        except OSError:
            pass


def _embed_io(stream, inputs: List[str]) -> Optional[List[List[float]]]:
    """Write one EmbedRequest, read one EmbedResponse, parse it.

    Wire format per inferd ADR 0017: NDJSON, single-frame request,
    single-frame response. Type tag on the response is "embeddings"
    on success, "error" on failure.
    """
    req = {
        "id": "cordon-filter",
        "input": inputs,
        "dimensions": EMBED_DIMENSIONS,
        "task": "clustering",
    }
    payload = (json.dumps(req) + "\n").encode("utf-8")
    try:
        stream.write(payload)
        stream.flush()
        line = stream.readline()
    except OSError:
        return None
    if not line:
        return None
    try:
        resp = json.loads(line)
    except (ValueError, TypeError):
        return None
    if resp.get("type") != "embeddings":
        return None
    embeddings = resp.get("embeddings")
    if not isinstance(embeddings, list):
        return None
    return embeddings


def _embed_all(sock_path: str, windows: List[str]) -> Optional[List[List[float]]]:
    """Batch the windows so we don't blow past inferd's frame cap."""
    out: List[List[float]] = []
    for i in range(0, len(windows), BATCH_SIZE):
        batch = windows[i : i + BATCH_SIZE]
        vectors = _embed_batch(sock_path, batch)
        if vectors is None or len(vectors) != len(batch):
            return None
        out.extend(vectors)
    return out


def _knn_scores(vectors) -> "list[float]":
    """Mean distance from each vector to its K nearest neighbours.

    Higher score = more isolated = more anomalous. Vectors are L2-
    normalised by inferd already (per the embed adapter's `embed_to_vec`),
    so cosine distance reduces to Euclidean / sqrt(2). We use Euclidean
    directly to keep the math transparent.
    """
    import numpy as np  # imported here so the top-level import-free path
    # is available for the early-bailout passthrough on systems without
    # numpy.

    arr = np.asarray(vectors, dtype=np.float32)
    n = arr.shape[0]
    k = min(K_NEIGHBOURS, n - 1)
    if k <= 0:
        return [0.0] * n
    # Pairwise squared distances. n in the low thousands → O(n^2) is
    # fine; we explicitly chose brute force over a KDTree to avoid
    # adding scipy.
    sq = ((arr[:, None, :] - arr[None, :, :]) ** 2).sum(axis=-1)
    np.fill_diagonal(sq, np.inf)
    # Take the k smallest distances per row, average them.
    nearest_k = np.partition(sq, k, axis=1)[:, :k]
    return np.sqrt(nearest_k).mean(axis=1).tolist()


_TOKEN_DIGITS = re.compile(r"\d+")
_TOKEN_HEX = re.compile(r"\b[0-9a-fA-F]{8,}\b")
_TOKEN_UUID = re.compile(
    r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}"
    r"-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b"
)
_TOKEN_IP = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")

# Keys we lift out of structured (JSON / Loki / OTel) records to build a
# discriminating signature. Order matters — most-discriminating fields
# come first so the 200-char cap doesn't cut them off. Path stems and
# msg stems vary per-record; service identifiers are mostly constant in
# a single log stream so they sit at the back.
_STRUCTURED_SHAPE_KEYS = (
    "level", "detected_level", "severity", "lvl",
    "method", "RequestMethod", "http.method",
    "status", "status_code", "DownstreamStatus", "OriginStatus",
    "RequestPath", "path", "url.path",
    "caller", "logger", "log.logger",
    "msg", "message", "error", "err",
    "action", "event",
    "RouterName", "ServiceName", "service_name", "service",
)


def _try_json(line: str) -> Optional[dict]:
    """Parse a single record as JSON; tolerate a leading log prefix.

    Loki / vector-style records sometimes prepend `ts="..."` before the
    payload. We don't try to be clever — just look for the first `{` and
    parse from there. None on failure.
    """
    s = line.lstrip()
    if not s or s[0] != "{":
        i = s.find("{")
        if i < 0:
            return None
        s = s[i:]
    try:
        obj = json.loads(s)
    except (ValueError, TypeError):
        return None
    return obj if isinstance(obj, dict) else None


def _flat_get(obj: dict, key: str) -> Optional[str]:
    """Return obj[key] if scalar, else look one level down in obj['labels']
    or obj['attributes'] (Loki / OTel shape). Returns string form or None.
    """
    v = obj.get(key)
    if v is None:
        for nest in ("labels", "attributes", "fields"):
            sub = obj.get(nest)
            if isinstance(sub, dict):
                v = sub.get(key)
                if v is not None:
                    break
    if v is None or isinstance(v, (dict, list)):
        return None
    return str(v)


def _status_class(status: Optional[str]) -> Optional[str]:
    """`200` → `2xx`, `503` → `5xx`. Bucketing keeps the signature stable
    across status codes that mean the same thing (404 vs 410, 502 vs 504).
    """
    if not status:
        return None
    digits = "".join(ch for ch in status if ch.isdigit())
    if len(digits) >= 3:
        return digits[0] + "xx"
    return None


def _path_stem(path: Optional[str]) -> Optional[str]:
    """Keep up to 4 path segments. Tokenise numeric / hex / UUID segments
    so `/api/users/42` and `/api/users/99` collapse to the same stem.
    """
    if not path:
        return None
    p = path.split("?", 1)[0].split("#", 1)[0]
    parts = [seg for seg in p.split("/") if seg]
    if not parts:
        return "/"
    out: List[str] = []
    for seg in parts[:4]:
        if _TOKEN_UUID.fullmatch(seg):
            out.append("<uuid>")
        elif _TOKEN_HEX.fullmatch(seg):
            out.append("<hex>")
        elif seg.isdigit():
            out.append("<n>")
        else:
            out.append(seg)
    return "/" + "/".join(out)


def _signature(line: str) -> str:
    """Stable kebab-case signature for a single line.

    Two paths:
      1. Structured (JSON-ish) records — lift discriminating keys
         (level, method, status-class, path-stem, caller, msg-stem)
         and join. This is what makes traefik / Loki access logs not
         collapse to a single signature, which would defeat cordon's
         purpose on structured-log fixtures.
      2. Plain text — token-replace then truncate. The 80-char prefix
         (was 40) gives unstructured records more room to differentiate
         once shared boilerplate is collapsed.
    """
    obj = _try_json(line)
    if obj is not None:
        parts: List[str] = []
        seen: set[str] = set()
        for k in _STRUCTURED_SHAPE_KEYS:
            v = _flat_get(obj, k)
            if v is None:
                continue
            if k in ("status", "status_code", "DownstreamStatus", "OriginStatus"):
                v = _status_class(v) or v
            elif k in ("RequestPath", "path", "url.path"):
                v = _path_stem(v) or v
            elif k in ("msg", "message", "error", "err"):
                # First two non-numeric words are usually the discriminating
                # phrase ("Retry exhausted", "tool rate limit", etc.).
                words = re.findall(r"[A-Za-z]+", v)[:3]
                v = "-".join(w.lower() for w in words) if words else ""
            else:
                v = v.lower()
            v = re.sub(r"[^A-Za-z0-9<>/-]+", "-", v).strip("-")
            if not v:
                continue
            tag = f"{k.lower().split('.')[-1]}={v}"
            if tag not in seen:
                seen.add(tag)
                parts.append(tag)
        if parts:
            sig = ";".join(parts)
            return sig[:200] if sig else "unknown"

    s = line.strip()
    s = _TOKEN_UUID.sub("<uuid>", s)
    s = _TOKEN_IP.sub("<ip>", s)
    s = _TOKEN_HEX.sub("<hex>", s)
    s = _TOKEN_DIGITS.sub("<n>", s)
    s = re.sub(r"[^A-Za-z0-9<>]+", "-", s).strip("-").lower()
    return s[:80] if s else "unknown"


_LEVEL_RE = re.compile(
    r"\b(fatal|panic|error|err|critical|crit|warn|warning|info|debug|trace)\b",
    re.IGNORECASE,
)
_LEVEL_NORMALISE = {
    "panic": "error",
    "err": "error",
    "critical": "error",
    "crit": "error",
    "warning": "warn",
}


def _level(line: str) -> str:
    obj = _try_json(line)
    if obj is not None:
        for k in ("level", "detected_level", "severity", "lvl"):
            v = _flat_get(obj, k)
            if v:
                lvl = v.strip().lower()
                return _LEVEL_NORMALISE.get(lvl, lvl) if lvl in _LEVEL_NORMALISE or lvl in _LEVEL_RANK else "unknown"
    m = _LEVEL_RE.search(line)
    if not m:
        return "unknown"
    lvl = m.group(1).lower()
    return _LEVEL_NORMALISE.get(lvl, lvl)


_LEVEL_RANK = {"error": 5, "fatal": 5, "warn": 4, "info": 3, "debug": 2, "trace": 1, "unknown": 0}


def _format_groups(
    selected_lines: Iterable[str], total_input_lines: int
) -> str:
    """Group the selected anomalous lines by signature, emit the
    compress prompt's exact output shape.
    """
    groups: dict[str, dict] = {}
    for line in selected_lines:
        sig = _signature(line)
        g = groups.get(sig)
        if g is None:
            groups[sig] = {
                "level": _level(line),
                "count": 1,
                "sample": line[:240] + ("…" if len(line) > 240 else ""),
                "lines": [line],
            }
        else:
            g["count"] += 1
            g["lines"].append(line)

    items = list(groups.items())
    items.sort(key=lambda kv: (-_LEVEL_RANK.get(kv[1]["level"], 0), -kv[1]["count"], kv[0]))

    out: List[str] = []
    for sig, g in items:
        # keys = load-bearing tokens that appeared in any line of the
        # group: paths, error codes, UUIDs, IPs, version strings.
        keys: List[str] = []
        seen: set[str] = set()
        for line in g["lines"]:
            for tok in _extract_keys(line):
                if tok not in seen:
                    seen.add(tok)
                    keys.append(tok)
                if len(keys) >= 12:
                    break
            if len(keys) >= 12:
                break
        keys_str = ",".join(keys)
        out.append(f"sig={sig}")
        out.append(f"level={g['level']}")
        out.append(f"count={g['count']}")
        out.append(f"sample={g['sample']}")
        out.append(f"keys={keys_str}")
        out.append("")
    out.append(f"tail={total_input_lines}→{len(items)}")
    return "\n".join(out) + "\n"


_KEY_TOKEN = re.compile(
    r"(?:[A-Za-z]:[\\/][^\s\"']+)"  # Windows path
    r"|(?:/[^\s\"']{2,})"  # Unix path
    r"|(?:\b\d{3}\b)"  # HTTP status code
    r"|(?:\b[A-Z]{2,}\d+\b)"  # error codes like E001, ERR42
    r"|(?:\bv?\d+\.\d+\.\d+(?:-[A-Za-z0-9.+-]+)?\b)"  # versions
)


def _extract_keys(line: str) -> List[str]:
    return _KEY_TOKEN.findall(line)


def compress(raw: str) -> str:
    debug = bool(os.environ.get("CORDON_DEBUG"))
    try:
        lines = [ln for ln in raw.splitlines() if ln.strip()]
        if debug:
            sys.stderr.write(f"cordon: non-empty lines={len(lines)} (need >= {WINDOW_SIZE*2})\n")
        if len(lines) < WINDOW_SIZE * 2:
            return raw

        try:
            import numpy  # noqa: F401  (verifies availability before any work)
        except ImportError:
            if debug:
                sys.stderr.write("cordon: numpy not available; passthrough\n")
            return raw

        windows = _windows(lines)
        if debug:
            sys.stderr.write(f"cordon: windows={len(windows)}\n")
        if len(windows) < 2:
            return raw

        # Uniformly sample windows down to MAX_WINDOWS to bound the O(n^2)
        # pairwise-distance step. Stride preserves coverage across the
        # whole input rather than truncating the tail.
        if MAX_WINDOWS > 0 and len(windows) > MAX_WINDOWS:
            step = len(windows) / float(MAX_WINDOWS)
            windows = [windows[int(i * step)] for i in range(MAX_WINDOWS)]

        sock_path = _embed_socket_path()
        texts = [w[1] for w in windows]
        vectors = _embed_all(sock_path, texts)
        if vectors is None:
            if debug:
                sys.stderr.write(
                    f"cordon: embed failed via {sock_path}; passthrough\n"
                )
            return raw

        scores = _knn_scores(vectors)

        # Pick the top-percentile windows. Score high = more isolated.
        n = len(scores)
        top_n = max(1, int(n * TOP_PERCENTILE / 100.0))
        ranked = sorted(range(n), key=lambda i: -scores[i])[:top_n]

        # Translate window indices back to source lines. A line is
        # "anomalous" if at least one of its containing windows is
        # in the top-percentile set. Dedupe in input order.
        flagged_starts = sorted(windows[i][0] for i in ranked)
        flagged_lines: List[str] = []
        seen_idx: set[int] = set()
        for start in flagged_starts:
            for offset in range(WINDOW_SIZE):
                idx = start + offset
                if idx not in seen_idx:
                    seen_idx.add(idx)
                    flagged_lines.append(lines[idx])

        return _format_groups(flagged_lines, len(lines))
    except Exception:
        if os.environ.get("CORDON_DEBUG"):
            import traceback
            traceback.print_exc(file=sys.stderr)
        return raw


def main() -> int:
    raw = sys.stdin.read()
    sys.stdout.write(compress(raw))
    return 0


if __name__ == "__main__":
    sys.exit(main())
