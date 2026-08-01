#!/usr/bin/env bash
# 场景 04：DNS 解析失败（egress 全拒导致无法访问 kube-dns）。
set -euo pipefail
SCENARIO="dns-failure"
source "$(dirname "$0")/../scripts/lib.sh"

case "${1:-}" in
  inject)
    ensure_namespace
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dns-app
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: dns-failure
spec:
  replicas: 1
  selector:
    matchLabels: {app: dns-app}
  template:
    metadata:
      labels:
        app: dns-app
        app.kubernetes.io/part-of: kubejojo-experiment
        kubejojo.io/scenario: dns-failure
    spec:
      containers:
      - name: app
        image: busybox:1.36
        command: ["sh", "-c", "sleep 3600"]
YAML
    kubectl -n "$NAMESPACE" apply -f - <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: dns-deny-egress
  namespace: kubejojo-lab
  labels:
    app.kubernetes.io/part-of: kubejojo-experiment
    kubejojo.io/scenario: dns-failure
spec:
  podSelector:
    matchLabels: {app: dns-app}
  policyTypes: [Egress]
  egress: []
YAML
    echo "injected dns-failure scenario"
    ;;
  verify)
    phase=$(run_probe "$SCENARIO" "dns-probe" "busybox:1.36" sh -c "nslookup kubernetes.default.svc.cluster.local || exit 1")
    if [ "$phase" = "Failed" ]; then
      echo "OK: DNS resolution denied (probe Failed)"
      exit 0
    fi
    echo "waiting: probe phase=$phase (DNS still works)"
    exit 1
    ;;
  cleanup)
    cleanup_scenario "$SCENARIO"
    echo "cleaned dns-failure"
    ;;
  *)
    echo "usage: $0 {inject|verify|cleanup}"
    exit 2
    ;;
esac
