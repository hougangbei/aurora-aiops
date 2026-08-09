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
  namespace: aurora-aiops-lab
  labels:
    app.kubernetes.io/part-of: aurora-aiops-experiment
    aurora-aiops.io/scenario: dns-failure
spec:
  replicas: 1
  selector:
    matchLabels: {app: dns-app}
  template:
    metadata:
      labels:
        app: dns-app
        app.kubernetes.io/part-of: aurora-aiops-experiment
        aurora-aiops.io/scenario: dns-failure
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
  namespace: aurora-aiops-lab
  labels:
    app.kubernetes.io/part-of: aurora-aiops-experiment
    aurora-aiops.io/scenario: dns-failure
spec:
  podSelector:
    matchLabels: {app: dns-app}
  policyTypes: [Egress]
  egress: []
YAML
    echo "injected dns-failure scenario"
    ;;
  verify)
    # nslookup 必须在策略覆盖的 dns-app pod 内执行，独立探针不受 NetworkPolicy 约束。
    kubectl -n "$NAMESPACE" wait --for=condition=Ready --timeout=90s pod -l app=dns-app >/dev/null 2>&1 || { echo "dns-app never ready"; exit 1; }
    pod="$(kubectl -n "$NAMESPACE" get pod -l app=dns-app -o jsonpath='{.items[0].metadata.name}')"
    if kubectl -n "$NAMESPACE" exec "$pod" -- nslookup kubernetes.default.svc.cluster.local >/dev/null 2>&1; then
      echo "waiting: DNS still works"
      exit 1
    fi
    echo "OK: DNS resolution denied"
    exit 0
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
