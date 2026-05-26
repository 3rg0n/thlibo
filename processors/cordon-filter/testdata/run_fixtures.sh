#!/usr/bin/env bash
# Run all 4 cordon-filter fixtures end-to-end and emit a report.
# Run from WSL: bash processors/cordon-filter/testdata/run_fixtures.sh
set -uo pipefail

OUT=/tmp/thlibo-fixtures/out
mkdir -p "$OUT"

REPO=/mnt/c/dev/Github/thlibo
PDF_DIR="/mnt/c/Users/ecopelan/OneDrive - Cisco/Documents/Workspace/AI.Transformation/pipeline/.references"
PDF1="$PDF_DIR/report.pdf"
PDF2="$PDF_DIR/ATC–SSE Integration (Phase 1) — PRD.pdf"
LOG1=/tmp/thlibo-fixtures/things-api-7d.jsonl
LOG2=/tmp/thlibo-fixtures/things-traefik-7d.jsonl

echo "=== fixtures present ==="
ls -la "$PDF1" "$PDF2" "$LOG1" "$LOG2" 2>&1
echo

run_pdf() {
  local label="$1"; local src="$2"
  local md="$OUT/${label}.md"
  local result="$OUT/${label}.cordon.txt"
  echo "=== $label : pdf-to-md ==="
  local t0=$(date +%s)
  python3 "$REPO/processors/pdf-to-md/run.py" < "$src" > "$md" 2>"$OUT/${label}.pdf-to-md.err"
  local t1=$(date +%s)
  local in_bytes=$(stat -c%s "$src")
  local md_bytes=$(stat -c%s "$md")
  local md_lines=$(wc -l <"$md")
  echo "  in=${in_bytes}B  md=${md_bytes}B  md_lines=${md_lines}  duration=$((t1-t0))s"

  echo "=== $label : cordon-filter ==="
  local t2=$(date +%s)
  CORDON_DEBUG=1 CORDON_MAX_WINDOWS=5000 python3 "$REPO/processors/cordon-filter/run.py" < "$md" > "$result" 2>"$OUT/${label}.cordon.err"
  local t3=$(date +%s)
  local out_bytes=$(stat -c%s "$result")
  echo "  cordon_in=${md_bytes}B  cordon_out=${out_bytes}B  duration=$((t3-t2))s"
  echo "  --- head of cordon output ---"
  head -30 "$result"
  echo "  --- groups surfaced ---"
  grep -c '^sig=' "$result" || true
  echo
}

run_log() {
  local label="$1"; local src="$2"
  local result="$OUT/${label}.cordon.txt"
  local in_bytes=$(stat -c%s "$src")
  local in_lines=$(wc -l <"$src")
  echo "=== $label : cordon-filter (CORDON_MAX_WINDOWS=5000) ==="
  echo "  in=${in_bytes}B  in_lines=${in_lines}"
  local t0=$(date +%s)
  CORDON_DEBUG=1 CORDON_MAX_WINDOWS=5000 python3 "$REPO/processors/cordon-filter/run.py" < "$src" > "$result" 2>"$OUT/${label}.cordon.err"
  local t1=$(date +%s)
  local out_bytes=$(stat -c%s "$result")
  echo "  cordon_out=${out_bytes}B  duration=$((t1-t0))s"
  echo "  --- head of cordon output ---"
  head -30 "$result"
  echo "  --- groups surfaced ---"
  grep -c '^sig=' "$result" || true
  echo
}

run_pdf "report" "$PDF1"
run_pdf "atc-sse-prd" "$PDF2"
run_log "things-api-7d" "$LOG1"
run_log "things-traefik-7d" "$LOG2"

echo "=== done ==="
