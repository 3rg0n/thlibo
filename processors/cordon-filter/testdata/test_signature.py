#!/usr/bin/env python3
"""Unit tests for cordon-filter signature extraction.

Run: python -m unittest processors/cordon-filter/testdata/test_signature.py

The traefik / Loki regression: every structured-access-log line was
collapsing to a single signature because _signature() truncated the
tokenised prefix at 40 chars, before any discriminating field appeared.
These tests pin the structured-record path so that doesn't recur.
"""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

# Add the cordon-filter directory to sys.path so we can import run.py
ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

import importlib.util  # noqa: E402

spec = importlib.util.spec_from_file_location("cordon_run", ROOT / "run.py")
cordon = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cordon)


def traefik_line(method: str, path: str, status: int, ip: str = "10.90.96.4") -> str:
    """Build a Loki-shaped traefik access log line, matching the real fixture."""
    return json.dumps({
        "ts": "1778538117644029594",
        "labels": {
            "ClientAddr": f"{ip}:55298",
            "ClientHost": ip,
            "DownstreamStatus": str(status),
            "RequestMethod": method,
            "RequestPath": path,
            "RouterName": "api@file",
            "ServiceName": "api@file",
            "level": "info",
            "detected_level": "info",
            "service_name": "things-traefik",
        },
    })


def things_api_line(caller: str, level: str, msg: str) -> str:
    """Things-API shape — what we saw in the things-api-7d fixture."""
    return json.dumps({
        "ts": "1778627939103000000",
        "labels": {
            "caller": caller,
            "level": level,
            "detected_level": level,
            "msg": msg,
            "service_name": "things-api",
        },
    })


class TestStructuredSignature(unittest.TestCase):
    """The fix: structured records must split by discriminating fields."""

    def test_traefik_methods_separate(self):
        get_sig = cordon._signature(traefik_line("GET", "/api/users", 200))
        post_sig = cordon._signature(traefik_line("POST", "/api/users", 200))
        self.assertNotEqual(get_sig, post_sig,
            f"GET vs POST collapsed to same sig: {get_sig!r}")

    def test_traefik_status_classes_separate(self):
        ok_sig = cordon._signature(traefik_line("GET", "/api/users", 200))
        err_sig = cordon._signature(traefik_line("GET", "/api/users", 503))
        self.assertNotEqual(ok_sig, err_sig,
            f"2xx vs 5xx collapsed: {ok_sig!r}")

    def test_traefik_status_codes_in_class_collapse(self):
        # Bucketing: 502 and 504 are both 5xx, so they SHOULD share a sig.
        s502 = cordon._signature(traefik_line("GET", "/api/users", 502))
        s504 = cordon._signature(traefik_line("GET", "/api/users", 504))
        self.assertEqual(s502, s504,
            f"502 and 504 should share a 5xx bucket: {s502!r} vs {s504!r}")

    def test_traefik_paths_separate(self):
        a = cordon._signature(traefik_line("GET", "/api/users", 200))
        b = cordon._signature(traefik_line("GET", "/api/projects", 200))
        self.assertNotEqual(a, b, f"different paths collapsed: {a!r}")

    def test_traefik_id_in_path_collapses(self):
        # /api/users/42 and /api/users/99 should share a sig
        # (path stem keeps up to 4 segments; trailing IDs unify).
        a = cordon._signature(traefik_line("GET", "/api/users/42", 200))
        b = cordon._signature(traefik_line("GET", "/api/users/99", 200))
        self.assertEqual(a, b,
            f"trailing-id paths should unify: {a!r} vs {b!r}")

    def test_things_api_callers_separate(self):
        a = cordon._signature(things_api_line("server/directory.go:366", "warn", "x"))
        b = cordon._signature(things_api_line("server/toolratelimit.go:154", "warn", "y"))
        self.assertNotEqual(a, b, f"different callers collapsed: {a!r}")

    def test_things_api_msg_stem_separates(self):
        a = cordon._signature(things_api_line(
            "jobs/queue.go:105", "warn",
            "Retry exhausted for task id=de594f47"))
        b = cordon._signature(things_api_line(
            "llm/client.go:732", "warn",
            "invalid character looking for beginning of value"))
        self.assertNotEqual(a, b, f"different msg stems collapsed: {a!r}")

    def test_traefik_synthetic_fixture_diverges(self):
        """Mimic the 543MB → 1-group regression: 50 traefik-shape lines
        across 5 distinct (method, status-class) combos must yield ≥5
        signatures, not 1.
        """
        sigs = set()
        combos = [
            ("GET", 200, "/api/users"),
            ("POST", 200, "/api/users"),
            ("GET", 404, "/api/users"),
            ("POST", 503, "/api/orders"),
            ("DELETE", 204, "/api/sessions"),
        ]
        for method, status, path in combos:
            for _ in range(10):  # 10 records per combo
                sigs.add(cordon._signature(traefik_line(method, path, status)))
        self.assertGreaterEqual(len(sigs), 5,
            f"only {len(sigs)} distinct sigs from 5 combos: {sigs}")


class TestStructuredLevel(unittest.TestCase):
    """Level extraction from structured records."""

    def test_traefik_level_from_labels(self):
        line = traefik_line("GET", "/api/users", 200)
        self.assertEqual(cordon._level(line), "info")

    def test_things_api_warn_level(self):
        line = things_api_line("x.go:1", "warn", "msg")
        self.assertEqual(cordon._level(line), "warn")

    def test_things_api_error_normalised(self):
        # 'panic' / 'critical' / 'err' should normalise to 'error'.
        line = things_api_line("x.go:1", "panic", "msg")
        self.assertEqual(cordon._level(line), "error")
        line = things_api_line("x.go:1", "ERR", "msg")
        self.assertEqual(cordon._level(line), "error")


class TestPlainTextSignature(unittest.TestCase):
    """Non-JSON inputs still go through the token-replace path."""

    def test_plain_log_line_still_works(self):
        a = cordon._signature(
            "ERROR  saml.callback: signature validation failed for "
            "assertion id=_a3f1c5; clock skew 412s exceeds tolerance"
        )
        b = cordon._signature(
            "INFO  knowledge-atlas: long-poll subscriber held open for "
            "894s on /v1/atlas/stream (sse, 124 events flushed)"
        )
        self.assertNotEqual(a, b)
        self.assertNotEqual(a, "unknown")

    def test_empty_line(self):
        self.assertEqual(cordon._signature(""), "unknown")
        self.assertEqual(cordon._signature("   "), "unknown")

    def test_malformed_json_falls_through(self):
        # `{ followed by garbage — should NOT crash, should hit the
        # plain-text branch.
        sig = cordon._signature("{not really json at all 200 GET /api/foo")
        self.assertNotEqual(sig, "unknown")
        # And digit token-replacement should still fire.
        self.assertIn("<n>", sig)


if __name__ == "__main__":
    unittest.main()
