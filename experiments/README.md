# 可重复故障实验

在 aurora-aiops 测试集群上注入可重复故障、验证故障真实出现、再清理的脚本集。所有对象都带 `app.kubernetes.io/part-of=aurora-aiops-experiment` 与 `aurora-aiops.io/scenario=<id>` 标签，且只存在于 `aurora-aiops-lab` 命名空间。

## 场景

| id | 故障 | 期望根因 | verify 判据 |
| --- | --- | --- | --- |
| 01 image-pull-error | 不存在的镜像 | image_pull_error | 容器等待 ImagePullBackOff / ErrImagePull |
| 02 crash-loop | 容器立即退出 | crash_loop | CrashLoopBackOff 或重启次数 >= 2 |
| 03 net-deny | NetworkPolicy 拒绝全部入站 | network_policy_deny | 探针 wget 目标失败 |
| 04 dns-failure | egress 全拒，无法访问 kube-dns | dns_resolution_failure | 探针 nslookup 失败 |
| 05 pvc-pending | 不存在的 StorageClass | pvc_pending | PVC phase = Pending |
| 06 resource-pressure | 请求远超节点容量 | resource_pressure | Pod Pending 且 Unschedulable |

## 用法

```bash
# 单个场景
./scenarios/01-image-pull-error.sh inject
./scenarios/01-image-pull-error.sh verify   # 故障真实出现时退出码为 0
./scenarios/01-image-pull-error.sh cleanup  # 只删除该场景带标签的资源

# 六个场景各 10 轮并记录 seed/时间/期望根因（TSV）
RUNS=10 ./scripts/run-suite.sh

# 一键清理全部实验资源
./scripts/reset-scenario.sh
```

## 安全边界

- cleanup 只按 `aurora-aiops.io/scenario=<id>` + `app.kubernetes.io/part-of=aurora-aiops-experiment` 标签删除，且限于 `aurora-aiops-lab` 命名空间，绝不动命名空间外资源。
- verify 在故障真实出现前返回非零；cleanup 后资源应消失。
