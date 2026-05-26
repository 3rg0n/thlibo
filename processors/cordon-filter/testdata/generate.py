#!/usr/bin/env python3
"""Generate a deterministic 1000-line fixture with 50 known anomalies.

Run: python testdata/generate.py > testdata/synthetic.log
     and the line numbers (1-indexed) of the 50 anomalies are written
     to testdata/synthetic.expected.

Background (950 lines): typical HTTP access log noise — 200/204/301
on a small set of normal paths, with churning IPs and timestamps.

Anomalies (50 lines): semantically out-of-distribution events salted
in at deterministic positions:
  - SQL error backtraces
  - certificate expiry warnings
  - SAML callback failures
  - long-poll knowledge-atlas streams (per the #27 traefik repro)
  - JVM OutOfMemoryError stack frames

Deterministic: seed is fixed; running twice produces byte-identical
output.
"""

from __future__ import annotations

import random
import sys
from pathlib import Path

random.seed(20260521)

NORMAL_PATHS = [
    "/api/v1/users",
    "/api/v1/projects",
    "/api/v1/tasks",
    "/static/app.js",
    "/static/main.css",
    "/healthz",
    "/metrics",
]
NORMAL_METHODS = ["GET", "POST"]
NORMAL_STATUSES = [200, 200, 200, 200, 204, 301]

ANOMALIES = [
    'ERROR  com.example.Repo.find(): java.sql.SQLException: '
    'connection reset by peer; SELECT * FROM users WHERE id=42',
    'WARN  cert-renewal: certificate "*.things.cisco.com" expires '
    'in 4d (notAfter=2026-05-25T00:00:00Z)',
    'ERROR  saml.callback: signature validation failed for '
    'assertion id=_a3f1c5; clock skew 412s exceeds tolerance',
    'INFO  knowledge-atlas: long-poll subscriber held open for '
    '894s on /v1/atlas/stream (sse, 124 events flushed)',
    'FATAL  jvm: java.lang.OutOfMemoryError: Java heap space at '
    'com.example.cache.LRUCache.put(LRUCache.java:117)',
]


def normal_line(i: int) -> str:
    method = random.choice(NORMAL_METHODS)
    path = random.choice(NORMAL_PATHS)
    status = random.choice(NORMAL_STATUSES)
    ip = f"10.{random.randint(0, 255)}.{random.randint(0, 255)}.{random.randint(0, 255)}"
    ts = 1747843200 + i  # 2026-05-21T16:00:00Z + i seconds
    return f'{ip} - - [ts={ts}] "{method} {path}" {status} 1432'


def main() -> int:
    n_total = 1000
    n_anom = 50
    anom_positions = sorted(random.sample(range(n_total), n_anom))
    anom_set = set(anom_positions)

    out = []
    expected = []
    for i in range(n_total):
        if i in anom_set:
            out.append(random.choice(ANOMALIES))
            expected.append(i + 1)  # 1-indexed
        else:
            out.append(normal_line(i))

    sys.stdout.write("\n".join(out) + "\n")

    expected_path = Path(__file__).with_name("synthetic.expected")
    expected_path.write_text("\n".join(str(n) for n in expected) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
