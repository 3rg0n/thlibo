#!/usr/bin/env python3
"""Mock inferd embed socket for offline cordon-filter testing.

Listens on the same socket path inferd v0.2.0 binds (per ADR 0017),
reads NDJSON EmbedRequest frames, returns deterministic pseudo-
embeddings derived from a hash of each input. Two windows with
similar text get similar vectors; rare windows are far away.

This exists *because* inferd v0.2.0's release build failed (no
platform tarballs shipped) — the wire shape is locked but no
binary is yet available to test against. Mock keeps cordon-filter
verifiable while inferd's release pipeline is fixed.

Usage (Linux/macOS):
    python testdata/mock_embed.py &
    python run.py < testdata/synthetic.log

Background-only; killed via Ctrl-C.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import socket
import struct
import sys
from typing import List


def _embed_socket_path() -> str:
    if sys.platform == "win32":
        return r"\\.\pipe\inferd-infer-embed"
    if sys.platform == "darwin":
        # /tmp fallback matches inferd ADR 0017 socket path.
        return os.path.join(
            os.environ.get("TMPDIR", "/tmp"), "inferd", "infer.embed.sock"  # nosec B108
        )
    xdg = os.environ.get("XDG_RUNTIME_DIR")
    if xdg:
        return os.path.join(xdg, "inferd", "infer.embed.sock")
    return os.path.join(os.path.expanduser("~"), ".inferd", "run", "infer.embed.sock")


DIM = 256


def _semantic_vector(text: str) -> List[float]:
    """Build a 256-dim vector that puts semantically-similar text close.

    Bag-of-words + hashed-feature trick: each token contributes to two
    deterministic dimensions. Two windows with overlapping vocab cluster.
    """
    import math

    vec = [0.0] * DIM
    tokens = re.findall(r"[A-Za-z]{2,}", text.lower())
    for tok in tokens:
        h = hashlib.blake2b(tok.encode("utf-8"), digest_size=4).digest()
        idx = struct.unpack(">I", h)[0] % DIM
        sign = 1.0 if (idx % 2 == 0) else -1.0
        vec[idx] += sign
        vec[(idx + 7) % DIM] += sign * 0.5

    # L2 normalise (matches what inferd's real embed adapter does).
    norm = math.sqrt(sum(v * v for v in vec)) or 1.0
    return [v / norm for v in vec]


def serve_unix(path: str) -> int:
    if os.path.exists(path):
        os.unlink(path)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(path)
    # 0o660 matches inferd's published embed-socket permissions (ADR 0017).
    os.chmod(path, 0o660)  # nosec B103
    srv.listen(4)
    sys.stderr.write(f"mock-embed listening at {path}\n")
    sys.stderr.flush()
    while True:
        conn, _ = srv.accept()
        with conn:
            f = conn.makefile("rwb", buffering=0)
            for line in f:
                if not line.strip():
                    continue
                try:
                    req = json.loads(line)
                except (ValueError, TypeError):
                    f.write(b'{"type":"error","id":"","code":"invalid_request","message":"bad json"}\n')
                    continue
                inputs = req.get("input", [])
                vectors = [_semantic_vector(s) for s in inputs]
                resp = {
                    "type": "embeddings",
                    "id": req.get("id", ""),
                    "embeddings": vectors,
                    "dimensions": DIM,
                    "model": "mock-embed-bow",
                    "usage": {"input_tokens": sum(len(s.split()) for s in inputs)},
                    "backend": "mock",
                }
                f.write((json.dumps(resp) + "\n").encode("utf-8"))
                f.flush()


def main() -> int:
    if sys.platform == "win32":
        sys.stderr.write("mock_embed: Windows named-pipe not implemented; use WSL\n")
        return 2
    return serve_unix(_embed_socket_path())


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(0)
