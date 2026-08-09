#!/usr/bin/env bash
# 场景 05：PVC 等待（不存在的 StorageClass -> Pending）。
set -euo pipefail
SCENARIO="pvc-pending"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data-pending
  namespace: aurora-aiops-lab
  labels:
    app.kubernetes.io/part-of: aurora-aiops-experiment
    aurora-aiops.io/scenario: pvc-pending
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: non-existent-sc
  resources:
    requests:
      storage: 1Gi
YAML
    echo "injected pvc-pending scenario"
    ;;
  verify)
    phase=$(kubectl -n "$NAMESPACE" get pvc data-pending -o jsonpath='{.status.phase}' 2>/dev/null || true)
    if [ "$phase" = "Pending" ]; then
      echo "OK: PVC pending"
      exit 0
    fi
    echo "waiting: phase=$phase"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned pvc-pending"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
