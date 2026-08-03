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
rg -q 'name: KUBEJOJO_RUNTIME_DIR' deploy/kubernetes/deployment.yaml || fail "runtime directory environment missing"
rg -q 'value: /var/run/kubejojo' deploy/kubernetes/deployment.yaml || fail "runtime directory value missing"
rg -q 'mountPath: /var/run/kubejojo' deploy/kubernetes/deployment.yaml || fail "runtime directory mount missing"
rg -q 'medium: Memory' deploy/kubernetes/deployment.yaml || fail "runtime directory must use an in-memory volume"
rg -q '^kind: ClusterRole$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole missing"
rg -q '^kind: ClusterRoleBinding$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRoleBinding missing"
if rg -q '"secrets"' deploy/kubernetes/rbac.yaml; then
  fail "readonly ClusterRole must not grant Secret access"
fi
rg -q '"pods/log"' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant pod log access"
rg -q '"persistentvolumes"' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant persistent volume access"
rg -q '"resourcequotas"' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant resource quota access"
rg -q '"limitranges"' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant limit range access"
rg -q 'apiGroups: \["rbac.authorization.k8s.io"\]' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant RBAC resource access"
rg -q 'apiGroups: \["autoscaling.k8s.io"\]' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole must grant VPA access"
rg -q 'key: bootstrap-admin-user' deploy/kubernetes/deployment.yaml || fail "bootstrap admin user Secret key missing"
rg -q 'key: bootstrap-admin-password' deploy/kubernetes/deployment.yaml || fail "bootstrap admin password Secret key missing"

printf 'kubernetes manifests verified\n'
