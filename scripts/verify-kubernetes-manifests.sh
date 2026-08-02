#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'manifest verification failed: %s\n' "$1" >&2
  exit 1
}

command -v kubectl >/dev/null 2>&1 || fail "kubectl is required"
command -v rg >/dev/null 2>&1 || fail "rg is required"

readonly_files=(
  deploy/kubernetes/namespace.yaml
  deploy/kubernetes/rbac.yaml
  deploy/kubernetes/pvc.yaml
  deploy/kubernetes/deployment.yaml
  deploy/kubernetes/service.yaml
)
for file in "${readonly_files[@]}" deploy/kubernetes/rbac-executor.yaml; do
  [[ -f "$file" ]] || fail "missing $file"
done

kubectl apply --dry-run=client --validate=false \
  -f deploy/kubernetes/namespace.yaml \
  -f deploy/kubernetes/rbac.yaml \
  -f deploy/kubernetes/pvc.yaml \
  -f deploy/kubernetes/deployment.yaml \
  -f deploy/kubernetes/service.yaml >/dev/null
kubectl apply --dry-run=client --validate=false \
  -f deploy/kubernetes/rbac-executor.yaml >/dev/null
kubectl auth reconcile --dry-run=client \
  -f deploy/kubernetes/rbac.yaml >/dev/null
kubectl auth reconcile --dry-run=client \
  -f deploy/kubernetes/rbac-executor.yaml >/dev/null

if rg -q 'KUBEJOJO_KUBECONFIG|kubejojo-kubeconfig|name: kubeconfig' deploy/kubernetes/deployment.yaml; then
  fail "deployment must not mount or select kubeconfig"
fi
rg -q '^kind: ClusterRole$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole missing"
rg -q '^kind: ClusterRoleBinding$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRoleBinding missing"
rg -q 'key: bootstrap-admin-user' deploy/kubernetes/deployment.yaml || fail "bootstrap admin user Secret key missing"
rg -q 'key: bootstrap-admin-password' deploy/kubernetes/deployment.yaml || fail "bootstrap admin password Secret key missing"

printf 'kubernetes manifests verified\n'
