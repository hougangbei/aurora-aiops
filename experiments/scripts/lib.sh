#!/usr/bin/env bash
# 公共函数。所有实验对象必须带 app.kubernetes.io/part-of=aurora-aiops-experiment
# 与 aurora-aiops.io/scenario=<id> 标签，且位于 aurora-aiops-lab 命名空间。

NAMESPACE="${NAMESPACE:-aurora-aiops-lab}"
PART_OF_LABEL="app.kubernetes.io/part-of=aurora-aiops-experiment"

ensure_namespace() {
  kubectl apply -f "$(dirname "$0")/../base/namespace.yaml" >/dev/null
}

# cleanup_scenario <scenario-id>：只删除带实验标签 + 场景标签、且位于实验
# 命名空间内的资源。绝不动命名空间之外或未带标签的对象。
cleanup_scenario() {
  local scenario="$1"
  local selector="aurora-aiops.io/scenario=$scenario,$PART_OF_LABEL"
  kubectl -n "$NAMESPACE" delete deploy -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" delete pod -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" delete networkpolicy -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" delete pvc -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" delete svc -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" delete configmap -l "$selector" --ignore-not-found >/dev/null 2>&1 || true
  # 残留的 verify 探针 pod 也一并清理。
  kubectl -n "$NAMESPACE" delete pod -l "aurora-aiops.io/probe=$scenario,$PART_OF_LABEL" --ignore-not-found >/dev/null 2>&1 || true
}

# run_probe <scenario> <name> <image> <command...>：启动一次性探针 pod 并等待
# 结束，输出其最终 phase（Succeeded 或 Failed）。
run_probe() {
  local scenario="$1" name="$2" image="$3"
  shift 3
  kubectl -n "$NAMESPACE" delete pod "$name" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" run "$name" \
    --image="$image" --restart=Never \
    --labels="aurora-aiops.io/probe=$scenario,$PART_OF_LABEL,app=$name" \
    --command -- "$@" >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" wait --for=jsonpath='{.status.phase}'=Succeeded --timeout=30s "pod/$name" >/dev/null 2>&1 \
    || kubectl -n "$NAMESPACE" wait --for=jsonpath='{.status.phase}'=Failed --timeout=30s "pod/$name" >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" get pod "$name" -o jsonpath='{.status.phase}'
}
