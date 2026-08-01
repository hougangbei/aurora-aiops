#!/usr/bin/env bash
# 清理全部实验资源：只删除 kubejojo-lab 命名空间内带实验标签的对象。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for script in "$ROOT"/scenarios/*.sh; do
  "$script" cleanup || true
done
kubectl get ns kubejojo-lab >/dev/null 2>&1 && kubectl delete ns kubejojo-lab --wait=false || true
echo "all experiment resources reset"
