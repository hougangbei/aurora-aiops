# qd → Aurora AIOps API 迁移矩阵

> 基线日期：2026-08-01。状态取值固定为 `reference`（后续计划迁移，当前仅参考）、`implemented-in-go`（已在 Go 中实现）、`intentionally-dropped`（不迁移）。

`qd/` 是只读迁移参考，本矩阵不构成对其代码的复制授权；Go 端全部行为按契约测试重新实现。迁移路径：`reference` → 对应计划实现后改为 `implemented-in-go`；明确舍弃的标为 `intentionally-dropped`。

## 说明

- **Go 目标命名空间统一为 `/api/v1`**；qd 的 `/api/...` 只作为行为参照，路径不等同于最终 Go 路径。
- aurora-aiops 自身已具备的资源域（节点、Pod、工作负载、网络、存储、RBAC 等）**不迁移 qd 的同名接口**，保留 aurora-aiops 既有实现；qd 中与 aurora-aiops 功能重叠的接口标 `intentionally-dropped`。
- 计划 01 已完成：Incident 持久化、状态机与三条 incidents 接口。
- 计划 01A 已完成：账号登录（Session）、集群连接与共享 kubeconfig。
- 计划 02 已完成：证据链（Evidence DAG / Context Bundle / K8s 采集）、五角色多智能体诊断工作流、模型调用（OpenAI-compatible）、Evidence / Runs / Reanalyze / SSE API；工具调用记录留待后续。
- 计划 04 将覆盖：审批、快照、故障实验。

## 认证 / 账号

| qd 接口 | 迁移状态 | 备注 |
| --- | --- | --- |
| `POST /api/auth/login` | `implemented-in-go` | `POST /api/v1/auth/login`，账号密码 + HttpOnly Session Cookie |
| `GET /api/auth/me` | `implemented-in-go` | `GET /api/v1/auth/me`，返回平台用户 `{id, username, role}` |
| `POST /api/auth/logout` | `implemented-in-go` | `POST /api/v1/auth/logout` |
| `GET /api/audit-logs` | `reference` | 计划 04 审批/审计能力 |

## 集群 / 节点 / 资源域

| qd 接口 | 迁移状态 | 备注 |
| --- | --- | --- |
| `GET /api/health` | `intentionally-dropped` | aurora-aiops 已有等价健康探针 |
| `GET /api/cluster/summary` | `intentionally-dropped` | aurora-aiops 集群总览已覆盖 |
| `GET /api/cluster/connection` | `implemented-in-go` | `GET /api/v1/cluster/connection`，共享 kubeconfig 探测（30s 缓存） |
| `PUT /api/cluster/connection` | `intentionally-dropped` | 连接参数改为进程启动时由环境变量固定，不再由前端改写 |
| `POST /api/cluster/connection/test` | `implemented-in-go` | `POST /api/v1/cluster/connection/test`，需 operator/admin |
| `POST /api/cluster/connection/refresh-kubeconfig` | `intentionally-dropped` | 共享 kubeconfig 由部署方管理，运行期不重刷 |
| `GET /api/cluster/capabilities` | `intentionally-dropped` | 迁移期能力探测，aurora-aiops 直接面向集群 |
| `GET /api/cluster/validation-resources` | `intentionally-dropped` | 交付物已归档，非运行时接口 |
| `GET /api/cluster/validation-resources/details` | `intentionally-dropped` | 同上 |
| `GET /api/nodes` | `implemented-in-go` | `GET /api/v1/nodes`，既有富 `NodeItem` 已携带 `internalAddress`/`hostname`（地址来自共享 `SelectNodeAddress`） |
| `GET /api/nodes/:name` | `intentionally-dropped` | aurora-aiops 节点详情已覆盖 |
| `GET /api/nodes/:name/metrics` | `intentionally-dropped` | aurora-aiops 指标能力已覆盖 |
| `GET /api/nodes/:name/snapshots` | `reference` | 计划 04 故障实验快照 |
| `POST /api/nodes/:name/snapshots` | `reference` | 计划 04 |
| `POST /api/nodes/:name/snapshots/:snapshotId/rollback` | `reference` | 计划 04 |
| `GET /api/namespaces` | `intentionally-dropped` | aurora-aiops 已覆盖 |
| `GET /api/namespaces/:id` | `intentionally-dropped` | 同上 |
| `GET /api/workloads` | `intentionally-dropped` | aurora-aiops 工作负载已覆盖 |
| `GET /api/workloads/:namespace/:kind/:name` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name` | `intentionally-dropped` | aurora-aiops Pod 详情已覆盖 |
| `GET /api/pods/:namespace/:name/logs` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name/events` | `intentionally-dropped` | 同上 |
| `GET /api/pods/:namespace/:name/network` | `intentionally-dropped` | 同上 |
| `GET /api/metrics/pods` | `intentionally-dropped` | aurora-aiops 已覆盖 |
| `GET /api/network/overview` | `intentionally-dropped` | aurora-aiops 网络域已覆盖 |
| `GET /api/network/flows` | `intentionally-dropped` | 同上 |
| `POST /api/network/health/recheck` | `intentionally-dropped` | 同上 |
| `GET /api/storage/overview` | `intentionally-dropped` | aurora-aiops 存储域已覆盖 |
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
| `POST /api/aiops/incidents/:id/reanalyze` | `implemented-in-go` | `POST /api/v1/aiops/incidents/:id/reanalyze`，限 `failed`/`rejected`/`resolved`，需 operator/admin |
| `POST /api/aiops/incidents/:id/approve-remediation` | `reference` | 计划 04 审批 |
| `POST /api/aiops/incidents/:id/reject-remediation` | `reference` | 计划 04 |
| `POST /api/aiops/incidents/:id/execute-remediation` | `reference` | 计划 04 |
| `POST /api/aiops/chat/sessions` | `reference` | 计划 02 ChatOps |
| `GET /api/aiops/chat/sessions/:id` | `reference` | 计划 02 |
| `GET /api/aiops/chat/sessions/:id/tool-calls` | `reference` | 计划 02 |

## 当前 Go 端已实现（计划 01 + 01A + 02）

| Go 接口 | 对应 qd 参考 | 说明 |
| --- | --- | --- |
| `POST /api/v1/aiops/incidents` | `POST /api/aiops/webhooks/alerts` | 创建 Incident 并自动触发五阶段诊断工作流，返回工作流结束后的最新状态 |
| `GET /api/v1/aiops/incidents` | `GET /api/aiops/incidents` | 列表，`updated_at DESC`，空列表为 `[]` |
| `GET /api/v1/aiops/incidents/:id` | `GET /api/aiops/incidents/:id` | 单条查询，404 `INCIDENT_NOT_FOUND` |
| `GET /api/v1/aiops/incidents/:id/evidence` | （无 qd 对应） | 证据 DAG `{nodes, edges}`；快照/事件/日志/Metrics，已脱敏 |
| `GET /api/v1/aiops/incidents/:id/runs` | （无 qd 对应） | 五角色 AgentRun 列表（role/attempt/status/output/tokens） |
| `POST /api/v1/aiops/incidents/:id/reanalyze` | `POST /api/aiops/incidents/:id/reanalyze` | 终态重诊断，需 operator/admin |
| `GET /api/v1/aiops/incidents/:id/events` | （无 qd 对应） | SSE 事件流，`lastEventId` 断线重放，15s 心跳 |

模型调用约定：`AURORA_AIOPS_LLM_BASE_URL/API_KEY/MODEL/API_STYLE/TIMEOUT`。五角色输出全部经结构化校验（置信度/证据引用/Shell 命令等），非法输出只把 Incident 置 `failed`，不执行动作。未配置模型时确定性 triage/collector 照常运行，根因及后续角色记为 `model_unavailable`。

文档与错误码见 `docs/aiops/api-v1.md`。

## 明确丢弃的 SSH / 节点直连耦合

qd 通过硬编码端口映射到节点并 SSH 执行 kubectl。该通道不属于 Kubernetes API 接入模型，且环境硬编码，全部 `intentionally-dropped`：

| qd 耦合 | 迁移状态 | 说明 |
| --- | --- | --- |
| `NODE_PORTS`（7788/7789/7790） | `intentionally-dropped` | 节点端口环境硬编码，Go 侧完全走 API Server |
| `ssh -p <port> root@127.0.0.1` 隧道 | `intentionally-dropped` | 后端不 SSH 任何节点 |
| `StrictHostKeyChecking=no` | `intentionally-dropped` | 不建立不安全主机信任 |
| 节点名 → 端口映射表 | `intentionally-dropped` | 节点发现只来自 Node API |

本阶段完成标准：用户只用平台账号密码登录；后端只创建一个共享 Kubernetes 客户端；节点 InternalIP 只来源于 Node API；关闭 qd SSH 隧道不影响任何 Go API；Metrics 缺失可降级；平台 actor 与共享 Kubernetes 身份在审计中可区分。
