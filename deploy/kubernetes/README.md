# Kubernetes 部署清单

默认安装是**集群范围只读**：后端通过 ServiceAccount 身份访问集群，不挂载、也不选择任何 kubeconfig。因此执行修复动作、回滚、Pod exec 等写操作会在 Kubernetes 授权层直接失败——这是刻意的最小权限默认。

## 安装

按顺序执行：

```sh
kubectl apply -f deploy/kubernetes/namespace.yaml
kubectl -n aurora-aiops create secret generic aurora-aiops-secrets \
  --from-literal=bootstrap-admin-user="$AURORA_AIOPS_ADMIN_USER" \
  --from-literal=bootstrap-admin-password="$AURORA_AIOPS_ADMIN_PASSWORD"
kubectl apply -f deploy/kubernetes/rbac.yaml
kubectl apply -f deploy/kubernetes/pvc.yaml
kubectl apply -f deploy/kubernetes/deployment.yaml
kubectl apply -f deploy/kubernetes/service.yaml
```

说明：

- `namespace.yaml` 创建 `aurora-aiops` 命名空间。
- `aurora-aiops-secrets` Secret 提供 `bootstrap-admin-user` / `bootstrap-admin-password`（users 表为空时创建首个 admin 账号）以及可选的 `llm-api-key`（配置模型端点时再写入）。
- `rbac.yaml` 只定义一个 `aurora-aiops-readonly` ServiceAccount 与对应的 ClusterRole / ClusterRoleBinding，覆盖集群范围只读权限。
- `pvc.yaml` 持久化 AIOps SQLite 数据（5Gi，`ReadWriteOnce`），不含集群特定的 StorageClass。
- `deployment.yaml` 使用 `serviceAccountName: aurora-aiops-readonly`，通过 in-cluster 身份访问集群；非 root、只读 rootfs、丢弃全部 capabilities。

## 启用执行权限（可选）

默认只读模式下，执行 / 回滚 / Pod exec 会在 Kubernetes 授权层失败。需要执行修复动作时：

```sh
kubectl apply -f deploy/kubernetes/rbac-executor.yaml
kubectl -n aurora-aiops patch deployment aurora-aiops \
  -p '{"spec":{"template":{"spec":{"serviceAccountName":"aurora-aiops-executor"}}}}'
```

`rbac-executor.yaml` 定义 `aurora-aiops-executor` ServiceAccount：

- 绑定只读 ClusterRole `aurora-aiops-readonly`；
- 绑定写 ClusterRole `aurora-aiops-executor-write`，只允许对 `apps/deployments` 与 `batch/cronjobs` 执行 `get/list/watch/update/patch`。

因此执行仍仅限 Deployment/CronJob 的更新与伸缩，**Pod exec、Secret 变更、命名空间变更等写操作仍然拒绝**。

## 校验

本地只读校验（不修改集群）：

```sh
./scripts/verify-kubernetes-manifests.sh
```
