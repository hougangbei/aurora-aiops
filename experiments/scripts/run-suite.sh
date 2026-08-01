#!/usr/bin/env bash
# 对六个故障场景各执行 inject -> verify -> cleanup，默认每场景 10 轮。
# 每条运行记录 seed、开始/结束时间与期望根因（TSV，便于论文分析）。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNS="${RUNS:-10}"
OUT="${OUT:-$ROOT/results/run-suite.tsv}"
mkdir -p "$(dirname "$OUT")"
echo "scenario seed start end duration_s expected_root_cause" > "$OUT"

root_cause() {
  case "$1" in
    image-pull) echo image_pull_error ;;
    crash-loop) echo crash_loop ;;
    net-deny) echo network_policy_deny ;;
    dns-failure) echo dns_resolution_failure ;;
    pvc-pending) echo pvc_pending ;;
    resource-pressure) echo resource_pressure ;;
  esac
}

for scenario in "$ROOT"/scenarios/*.sh; do
  id="$(basename "$scenario" .sh)"
  id="${id#??-}"
  cause="$(root_cause "$id")"
  for ((i = 1; i <= RUNS; i++)); do
    seed="$RANDOM$RANDOM"
    start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    start_s="$(date +%s)"
    echo "== $id run $i/$RUNS seed=$seed"
    "$scenario" inject >/dev/null
    "$scenario" verify
    "$scenario" cleanup >/dev/null
    end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    end_s="$(date +%s)"
    printf "%s %s %s %s %d %s\n" "$id" "$seed" "$start" "$end" "$((end_s - start_s))" "$cause" >> "$OUT"
  done
done

echo "suite complete -> $OUT"
