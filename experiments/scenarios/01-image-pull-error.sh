#!/usr/bin/env bash
# 场景 01：镜像拉取失败（ImagePullBackOff）。
set -euo pipefail
SCENARIO="image-pull"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: image-pull-demo
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: image-pull
spec:
  replicas: 1
  selector:
    matchLabels: {app: image-pull-demo}
  template:
    metadata:
      labels:
        app: image-pull-demo
        app.kubernetes.io/part-of: kubejojo-experiment
        kubejojo.io/scenario: image-pull
    spec:
      containers:
      - name: app
        image: registry.invalid/does-not-exist:v9
YAML
    echo "injected image-pull scenario"
    ;;
  verify)
    for _ in $(seq 1 60); do
      reason=$(kubectl -n "$NAMESPACE" get pod -l kubejojo.io/scenario=image-pull -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || true)
      if [ "$reason" = "ImagePullBackOff" ] || [ "$reason" = "ErrImagePull" ]; then
        echo "OK: ImagePullBackOff present"
        exit 0
      fi
      sleep 2
    done
    echo "waiting: reason=$reason"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned image-pull"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
