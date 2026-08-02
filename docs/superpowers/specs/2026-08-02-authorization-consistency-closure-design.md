# 权限与一致性封口设计

**状态：** 已确认设计方向，等待书面规格复核
**日期：** 2026-08-02
**对应验收：** `dsglm开发文档` 计划 01A、02、04 的安全与交付缺口

## 1. 目标

本阶段只解决会阻止系统安全上线的权限、并发状态和部署问题：

1. 平台角色真实约束所有 HTTP 写操作，`viewer` 无法借共享 kubeconfig 修改集群或系统。
2. Incident 状态迁移使用数据库比较并交换（compare-and-swap），并发决策最多一个成功。
3. 修复风险由确定性 policy 计算；模型只能提供建议和附加 blocker，不能降低风险或绕过批准限制。
4. Kubernetes 清单可以以默认只读模式启动，并提供显式启用的有限执行权限。

完成后，系统仍保持单 Go 进程、共享 Kubernetes 客户端、SQLite 和现有 React 前端，不引入新的基础设施。本地运行继续支持 kubeconfig；集群内运行默认使用 Pod ServiceAccount 身份。

## 2. 非目标

以下内容放入第二阶段“功能与实验封口”，本阶段不混入实现：

- 模型设置持久化 API 和连接测试。
- 审计记录查询 API 与真实审计页面。
- Incident 列表筛选、rollback 前端参数和执行页面补齐。
- 前端依赖升级、API 文档总更新和 bundle 拆分。
- 三节点集群六类故障各 10 次、响应式浏览器验收和论文数据导出。

## 3. HTTP 权限模型

所有路由先经过 `RequireSession`，再按能力附加角色守卫。不能再把“已登录”视为“可写”。

| 能力 | viewer | operator | admin |
| --- | --- | --- | --- |
| GET/HEAD 只读资源、Incident、Evidence、Runs、Audit | 允许 | 允许 | 允许 |
| 创建 Incident、reanalyze、approve、reject、主动连接探测 | 拒绝 | 允许 | 允许 |
| execute、rollback、实验运行写入 | 拒绝 | 拒绝 | 允许 |
| Pod exec、原始 Manifest、资源 PUT/POST/DELETE、系统 update/restart/rollback | 拒绝 | 拒绝 | 允许 |

实现方式：

- 在 `server/internal/server` 中定义可复用的 `RequireOperator` 和 `RequireAdmin` 组合，不复制角色列表。
- 注册路由时显式挂载守卫；不得依赖前端隐藏按钮。
- 增加路由清单测试，枚举所有 POST、PUT、PATCH、DELETE 和 WebSocket exec 路由，验证 viewer/operator/admin 的期望状态码。
- 未登录保持 `401 UNAUTHORIZED`；已登录但角色不足统一返回 `403 FORBIDDEN`。

## 4. 原子状态迁移

把 Repository 的无条件 `UpdateStatus(id, to)` 替换为带旧状态的条件更新：

```sql
UPDATE incidents
SET status = ?, updated_at = ?
WHERE id = ? AND status = ?
```

Service 流程固定为：读取 Incident、使用状态机验证 `from -> to`、执行条件更新。影响行数为零时再查询一次：不存在返回 `INCIDENT_NOT_FOUND`，状态已变化返回 `STATE_TRANSITION_CONFLICT`。

所有 approve、reject、execute、workflow advance 和失败转移复用同一个原子接口。并发 approve/reject 测试必须证明：一个请求成功，另一个得到冲突；最终状态和审计记录只对应成功决策。

## 5. 确定性风险门禁

风险数据分为两层：

- `modelReview`：模型给出的风险说明、blocker 和建议，只用于展示。
- `policyReview`：后端对每个结构化 Action 调用 `policy.Evaluate` 得到的允许状态、风险和原因，是唯一执行依据。

后端形成并持久化统一的 `EffectiveRiskReview`：

```json
{
  "effectiveRisk": "medium",
  "approvable": true,
  "blockers": [],
  "actions": [
    {"kind": "restart_deployment", "allowed": true, "risk": "medium", "reason": "restart deployment"}
  ],
  "modelReview": {"riskLevel": "low", "approved": true}
}
```

规则如下：

1. 任一 Action 缺少结构化字段或被 policy 拒绝时，`approvable=false`。
2. `effectiveRisk` 是所有 policy 结果的最高风险；模型不能降低它。
3. policy blocker 与模型 blocker 合并展示，但 policy blocker 不可被模型覆盖。
4. `Approve` 在状态变更前重新计算 policy，防止旧页面或直接 API 调用绕过门禁。
5. 前端审批对话框只读取 `approvable` 和 `effectiveRisk`；不可批准时只显示拒绝操作。
6. `Execute` 保留现有执行前 policy 复核，作为第二道防线。

模型输出非法时继续进入 `failed`，不得产生批准或执行动作。

## 6. Kubernetes 部署权限

默认清单与可执行清单分离：

- `deploy/kubernetes/rbac.yaml`：只包含 `kubejojo-readonly` ServiceAccount、只读 `ClusterRole` 和 `ClusterRoleBinding`，支持 Nodes、Namespaces 及跨命名空间读取。
- `deploy/kubernetes/rbac-executor.yaml`：显式启用的 `kubejojo-executor` ServiceAccount；同时绑定只读 ClusterRole，并额外绑定仅允许 Deployment restart/scale 和 CronJob suspend 所需 verbs 的执行 ClusterRole。
- `deploy/kubernetes/deployment.yaml` 默认继续使用 readonly ServiceAccount。
- Kubernetes 客户端配置顺序调整为：显式 `KUBEJOJO_KUBECONFIG`、显式 `KUBECONFIG`、集群内 `rest.InClusterConfig()`、本地 `~/.kube/config`。Deployment 不再强制挂载 kubeconfig Secret，确保实际身份就是所选 ServiceAccount；进程内仍只创建一个共享客户端。
- 新增 Namespace 与 PVC 清单。部署文档提供创建 bootstrap 管理员和可选 LLM Key Secret 的准确命令，示例不包含真实凭证；集群外部署才说明如何显式挂载 kubeconfig。
- Deployment 从 Secret 引用 `KUBEJOJO_BOOTSTRAP_ADMIN_USER` 和 `KUBEJOJO_BOOTSTRAP_ADMIN_PASSWORD`，保证空数据库首次启动有明确配置入口。

执行权限仍受应用 policy 与人工审批约束；Kubernetes RBAC 是最后一道权限上限，不替代应用层角色检查。

## 7. 错误处理与审计

- 角色不足：`403 FORBIDDEN`，不调用业务 Service。
- 状态被并发改变：`409 STATE_TRANSITION_CONFLICT`，客户端刷新 Incident 后重试人工决策。
- policy 不允许批准：`403 ACTION_NOT_ALLOWED`，响应只返回脱敏后的规则原因。
- 只有成功的 approve/reject 写决策审计；冲突请求不得产生误导性审计记录。
- execute 失败继续保留快照并记录失败审计，未知写状态不得自动重试。

## 8. 测试与验收

### 自动测试

1. 路由权限矩阵覆盖所有写路由和 Pod exec。
2. `go test -race ./internal/aiops ./internal/remediation ./internal/server -count=20` 覆盖并发状态决策。
3. 风险测试覆盖“模型报 low、policy 拒绝”“模型允许但结构化字段缺失”“policy medium 正常批准”。
4. 前端测试验证批准按钮完全由后端 `approvable` 控制。
5. Kubernetes 客户端测试覆盖显式 kubeconfig、集群内配置和本地回退的优先级，且错误信息不泄露路径或凭证。
6. `kubectl auth reconcile --dry-run=client` 验证 RBAC 清单结构。
7. 完整执行 `go vet ./...`、`go test -race ./...`、`npm test`、`npm run build` 和 release 构建。

### 集群冒烟

在隔离测试集群分别部署 readonly 和 executor 模式并运行 `kubectl auth can-i`：

- readonly：跨命名空间 get/list/watch 成功，所有写操作失败。
- executor：读取成功，仅允许目标 Deployment/CronJob 的有限 patch/update；Namespace 删除、Secret 写入、Pod exec 等仍失败。
- viewer 即使后端使用 executor kubeconfig，所有应用写 API 仍返回 403。

## 9. 完成定义

本阶段只有在以下条件同时满足时完成：

- viewer 无法调用任何集群、系统或 AIOps 写接口。
- operator 只能执行明确列出的 Incident 工作流操作。
- 并发状态决策最多一个成功，数据库和审计一致。
- policy 是风险和可批准性的唯一权威来源。
- 默认部署使用 readonly ServiceAccount 身份启动，executor 权限必须显式启用且保持最小化。
- 自动测试、release 构建和权限矩阵冒烟全部通过。
