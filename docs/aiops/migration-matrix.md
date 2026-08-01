# qd → kubejojo API 迁移矩阵

> 基线日期：2026-08-01。状态取值固定为 `reference`（后续计划迁移，当前仅参考）、`implemented-in-go`（已在 Go 中实现）、`intentionally-dropped`（不迁移）。

`qd/` 是只读迁移参考，本矩阵不构成对其代码的复制授权；Go 端全部行为按契约测试重新实现。迁移路径：`reference` → 对应计划实现后改为 `implemented-in-go`；明确舍弃的标为 `intentionally-dropped`。

## 说明

- **Go 目标命名空间统一为 `/api/v1`**；qd 的 `/api/...` 只作为行为参照，路径不等同于最终 Go 路径。
- kubejojo 自身已具备的资源域（节点、Pod、工作负载、网络、存储、RBAC 等）**不迁移 qd 的同名接口**，保留 kubejojo 既有实现；qd 中与 kubejojo 功能重叠的接口标 `intentionally-dropped`。
- 计划 01 已完成：Incident 持久化、状态机与三条 incidents 接口。
- 计划 01A 将覆盖：账号登录（Session）、集群连接与共享 kubeconfig。
- 计划 02 将覆盖：证据链、多智能体诊断、模型调用、工具调用记录。
- 计划 04 将覆盖：审批、快照、故障实验。

## 认证 / 账号

| qd 接口 | 迁移状态 | 备注 |
| --- | --- | --- |
| `POST /api/auth/login` | `reference` | 计划 01A 用本地账号密码 + Session 替换 |
| `GET /api/auth/me` | `reference` | 计划 01A |
| `POST /api/auth/logout` | `reference` | 计划 01A |
| `GET /api/audit-logs` | `reference` | 计划 04 审批/审计能力 |

## 集群 / 节点 / 资源域

| qd 接口 | 迁移状态 | 备注 |
| --- | --- | --- |
| `GET /api/health` | `intentionally-dropped` | kubejojo 已有等价健康探针 |
| `GET /api/cluster/summary` | `intentionally-dropped` | kubejojo 集群总览已覆盖 |
| `GET /api/cluster/connection` | `reference` | 计划 01A 共享 kubeconfig 与连接探测 |
| `PUT /api/cluster/connection` | `reference` | 计划 01A |
| `POST /api/cluster/connection/test` | `reference` | 计划 01A |
| `POST /api/cluster/connection/refresh-kubeconfig` | `reference` | 计划 01A |
| `GET /api/cluster/capabilities` | `intentionally-dropped` | 迁移期能力探测，kubejojo 直接面向集群 |
| `GET /api/cluster/validation-resources` | `intentionally-dropped` | 交付物已归档，非运行时接口 |
| `GET /api/cluster/validation-resources/details` | `intentionally-dropped` | 同上 |
| `GET /api/nodes` | `intentionally-dropped` | kubejojo 节点列表已覆盖 |
| `GET /api/nodes/:name` | `intentionally-dropped` | 同上 |
| `GET /api/nodes/:name/metrics` | `intentionally-dropped` | kubejojo 指标能力已覆盖 |
| `GET /api/nodes/:name/snapshots` | `reference` | 计划 04 故障实验快照 |
| `POST /api/nodes/:name/snapshots` | `reference` | 计划 04 |
| `POST /api/nodes/:name/snapshots/:snapshotId/rollback` | `reference` | 计划 04 |
| `GET /api/namespaces` | `intentionally-dropped` | kubejojo 已覆盖 |
| `GET /api/namespaces/:id` | `intentionally-dropped` | 同上 |
| `GET /api/workloads` | `intentionally-dropped` | kubejojo 工作负载已覆盖 |
| `GET /api/workloads/:namespace/:kind/:name` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name` | `intentionally-dropped` | kubejojo Pod 详情已覆盖 |
| `GET /api/pods/:namespace/:name/logs` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name/events` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name/network` | `intentionally-dropped` | 同上 |
| `GET /api/metrics/pods` | `intentionally-dropped` | kubejojo 已覆盖 |
| `GET /api/network/overview` | `intentionally-dropped` | kubejojo 网络域已覆盖 |
| `GET /api/network/flows` | `intentionally-dropped` | 同上 |
| `POST /api/network/health/recheck` | `intentionally-dropped` | 同上 |
| `GET /api/storage/overview` | `intentionally-dropped` | kubejojo 存储域已覆盖 |
| `GET /api/storage/classes` | `intentionally-dropped` | 同上 |
| `GET /api/storage/persistent-volumes` | `intentionally-dropped` | 同上 |
| `GET /api/storage/persistent-volume-claims` | `intentionally-dropped` | 同上 |
| `POST /api/storage/classes/:name/set-default` | `intentionally-dropped` | 同上 |

## AIOps 告警 / Incident / 工具 / 模型 / ChatOps

| qd 接口 | 迁移状态 | 备注 |
| --- | --- | --- |
| `GET /api/alerts` | `reference` | 计划 02 证据链接入后由 Incident 取代 |
| `GET /api/alerts/:id` | `reference` | 同上 |
| `GET /api/ai/alerts/:id/snapshots` | `reference` | 计划 04 执行前快照 |
| `POST /api/ai/alerts/:id/approve` | `reference` | 计划 04 审批 |
| `POST /api/ai/alerts/:id/reject` | `reference` | 计划 04 |
| `POST /api/ai/alerts/:id/manual-execute` | `reference` | 计划 04 人工执行 |
| `GET /api/ai/providers/current` | `reference` | 计划 02 模型配置 |
| `PUT /api/ai/providers/current` | `reference` | 计划 02 |
| `POST /api/ai/providers/current/test` | `reference` | 计划 02 |
| `GET /api/aiops/integrations` | `reference` | 计划 02 集成配置 |
| `PUT /api/aiops/integrations/:provider` | `reference` | 计划 02 |
| `POST /api/aiops/integrations/:provider/test` | `reference` | 计划 02 |
| `GET /api/aiops/incidents` | `implemented-in-go` | `GET /api/v1/aiops/incidents` |
| `POST /api/aiops/webhooks/alerts` | `implemented-in-go` | 告警进入 → `POST /api/v1/aiops/incidents` |
| `GET /api/aiops/incidents/:id` | `implemented-in-go` | `GET /api/v1/aiops/incidents/:id` |
| `GET /api/aiops/incidents/:id/tool-calls` | `reference` | 计划 02 工具调用记录 |
| `POST /api/aiops/incidents/:id/reanalyze` | `reference` | 计划 02 重诊断 |
| `POST /api/aiops/incidents/:id/approve-remediation` | `reference` | 计划 04 审批 |
| `POST /api/aiops/incidents/:id/reject-remediation` | `reference` | 计划 04 |
| `POST /api/aiops/incidents/:id/execute-remediation` | `reference` | 计划 04 |
| `POST /api/aiops/chat/sessions` | `reference` | 计划 02 ChatOps |
| `GET /api/aiops/chat/sessions/:id` | `reference` | 计划 02 |
| `GET /api/aiops/chat/sessions/:id/tool-calls` | `reference` | 计划 02 |

## 当前 Go 端已实现（计划 01）

| Go 接口 | 对应 qd 参考 | 说明 |
| --- | --- | --- |
| `POST /api/v1/aiops/incidents` | `POST /api/aiops/webhooks/alerts` | 创建 Incident，初始状态 `received` |
| `GET /api/v1/aiops/incidents` | `GET /api/aiops/incidents` | 列表，`updated_at DESC`，空列表为 `[]` |
| `GET /api/v1/aiops/incidents/:id` | `GET /api/aiops/incidents/:id` | 单条查询，404 `INCIDENT_NOT_FOUND` |

文档与错误码见 `docs/aiops/api-v1.md`。
