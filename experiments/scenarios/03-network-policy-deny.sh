#!/usr/bin/env bash
# 场景 03：NetworkPolicy 拒绝全部入站（网络隔离）。
set -euo pipefail
SCENARIO="net-deny"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: net-target
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: net-deny
spec:
  replicas: 1
  selector:
    matchLabels: {app: net-target}
  template:
    metadata:
      labels:
        app: net-target
        app.kubernetes.io/part-of: kubejojo-experiment
        kubejojo.io/scenario: net-deny
    spec:
      containers:
      - name: nginx
        image: nginx:1.27
YAML
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: v1
kind: Service
metadata:
  name: net-target
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: net-deny
spec:
  selector:
    app: net-target
  ports:
  - port: 80
YAML
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: net-deny-all
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: net-deny
spec:
  podSelector:
    matchLabels: {app: net-target}
  policyTypes: [Ingress]
  ingress: []
YAML
    echo "injected net-deny scenario"
    ;;
  verify)
    kubectl -n "$NAMESPACE" wait --for=condition=Ready --timeout=90s pod -l app=net-target >/dev/null 2>&1 || { echo "target never ready"; exit 1; }
    phase=$(run_probe "$SCENARIO" "net-probe" "busybox:1.36" sh -c "wget -qO- --timeout=2 http://net-target/ || exit 1")
    if [ "$phase" = "Failed" ]; then
      echo "OK: ingress denied (probe Failed)"
      exit 0
    fi
    echo "waiting: probe phase=$phase (policy not effective yet)"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned net-deny"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
