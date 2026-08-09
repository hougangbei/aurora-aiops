#!/usr/bin/env bash
# 场景 06：资源压力（请求远超节点容量 -> Unschedulable Pending）。
set -euo pipefail
SCENARIO="resource-pressure"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: v1
kind: Pod
metadata:
  name: pressure-demo
  namespace: aurora-aiops-lab
  labels:
    app.kubernetes.io/part-of: aurora-aiops-experiment
    aurora-aiops.io/scenario: resource-pressure
spec:
  containers:
  - name: app
    image: busybox:1.36
    command: ["sh", "-c", "sleep 3600"]
    resources:
      requests:
        cpu: "100"
        memory: "200Gi"
YAML
    echo "injected resource-pressure scenario"
    ;;
  verify)
    for _ in $(seq 1 30); do
      phase=$(kubectl -n "$NAMESPACE" get pod pressure-demo -o jsonpath='{.status.phase}' 2>/dev/null || true)
      reason=$(kubectl -n "$NAMESPACE" get pod pressure-demo -o jsonpath='{.status.conditions[?(@.type=="PodScheduled")].reason}' 2>/dev/null || true)
      if [ "$phase" = "Pending" ] && [ "$reason" = "Unschedulable" ]; then
        echo "OK: pod unschedulable (Pending)"
        exit 0
      fi
      sleep 2
    done
    echo "waiting: phase=$phase reason=$reason"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned resource-pressure"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
