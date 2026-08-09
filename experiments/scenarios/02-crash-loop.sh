#!/usr/bin/env bash
# 场景 02：容器崩溃循环（CrashLoopBackOff）。
set -euo pipefail
SCENARIO="crash-loop"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: crash-loop-demo
  namespace: aurora-aiops-lab
  labels:
    app.kubernetes.io/part-of: aurora-aiops-experiment
    aurora-aiops.io/scenario: crash-loop
spec:
  replicas: 1
  selector:
    matchLabels: {app: crash-loop-demo}
  template:
    metadata:
      labels:
        app: crash-loop-demo
        app.kubernetes.io/part-of: aurora-aiops-experiment
        aurora-aiops.io/scenario: crash-loop
    spec:
      containers:
      - name: app
        image: busybox:1.36
        command: ["sh", "-c", "echo BOOM; sleep 2; exit 1"]
YAML
    echo "injected crash-loop scenario"
    ;;
  verify)
    # 返回 0 当且仅当出现崩溃循环（重启次数 >= 2 或 CrashLoopBackOff）。
    for _ in $(seq 1 60); do
      restarts=$(kubectl -n "$NAMESPACE" get pod -l aurora-aiops.io/scenario=crash-loop -o jsonpath='{.items[0].status.containerStatuses[0].restartCount}' 2>/dev/null || true)
      reason=$(kubectl -n "$NAMESPACE" get pod -l aurora-aiops.io/scenario=crash-loop -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || true)
      if [ "$reason" = "CrashLoopBackOff" ] || { [ "${restarts:-0}" -ge 2 ]; }; then
        echo "OK: CrashLoopBackOff present (restarts=$restarts)"
        exit 0
      fi
      sleep 2
    done
    echo "waiting: reason=$reason restarts=$restarts"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned crash-loop"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
