# AIOps API v1

本页记录 kubejojo Go 后端已实现的 `/api/v1` 接口。当前阶段（计划 01 + 01A + 02）落地了 Incident 的创建与查询、平台账号登录、共享集群连接状态与节点发现、证据链与多智能体诊断工作流（Evidence / AgentRun / Reanalyze / SSE 事件流）；审批、执行、工具调用、ChatOps、集成配置在后续计划中实现。

所有接口返回统一信封 `{code, message, data}`：

- 成功：`code = "OK"`，`message = "success"`，`data` 为资源或资源数组（空列表必须是 `[]`，不会是 `null`）。
- 失败：`code` 为业务错误码，`message` 为可读错误信息，`data` 省略。

## 认证入口

平台使用账号密码 + 服务端 Session。`POST /api/v1/auth/login` 是 `/api/v1` 下唯一匿名入口，登录成功后通过 `Set-Cookie: kubejojo_session`（HttpOnly + SameSite=Lax）建立会话。其余接口均需携带该 Cookie；后端通过共享 kubeconfig 访问集群，**不再接收任何请求级 Kubernetes Token**。前端 Axios 使用 `withCredentials: true` 同源携带 Cookie。

### 账号 API

- `POST /api/v1/auth/login`：请求体 `{username, password}`。成功 200，`data` 为 `{id, username, role, expiresAt}` 并设置 Session Cookie；失败统一 401 `INVALID_CREDENTIALS`（未知用户与错误密码不可区分）。
- `GET /api/v1/auth/me`：需要 Session，返回当前 `{id, username, role}`。
- `POST /api/v1/auth/logout`：需要 Session，删除服务端会话并清空 Cookie。

### 集群连接 / 节点 API

- `GET /api/v1/cluster/connection`：返回最近一次探测缓存（最长 30 秒）。`connected` / `degraded` 返回 200；`unreachable` 返回 503 `CLUSTER_UNREACHABLE`；权限不足返回 503 `CLUSTER_PERMISSION_DENIED`。Metrics 不可用只降级，不判定离线。
- `POST /api/v1/cluster/connection/test`：立即探测，需 `operator` / `admin`；并发探测合并为一次。
- `GET /api/v1/nodes`：返回节点数组，每项是既有富模型 `NodeItem` 的超集（含 `internalAddress`、`hostname`，其中 `ip`/`internalAddress` 来自共享 `cluster.SelectNodeAddress`），空数组为 `[]`；不执行任何主机命令。

架构与安全边界见 `docs/architecture/single-cluster-access.md`。

## 通用错误码

| HTTP | code | 说明 |
| --- | --- | --- |
| 400 | `INVALID_INCIDENT_REQUEST` | 请求体格式不正确，或业务参数校验失败（见下方各接口） |
| 404 | `INCIDENT_NOT_FOUND` | 指定 ID 的 Incident 不存在 |
| 500 | `CREATE_INCIDENT_FAILED` | 创建失败（不向客户端泄漏内部错误） |
| 500 | `LIST_INCIDENTS_FAILED` | 列表查询失败 |
| 500 | `GET_INCIDENT_FAILED` | 单条查询失败 |

## 领域模型

### Incident

```json
{
  "id": "inc-9f1c2b3d4e5f6071",
  "summary": "Pod crash loop",
  "severity": "critical",
  "status": "received",
  "namespace": "default",
  "resourceKind": "Pod",
  "resourceName": "api-0",
  "createdAt": "2026-08-01T12:00:00.123456Z",
  "updatedAt": "2026-08-01T12:00:00.123456Z"
}
```

- `id`：服务端生成，格式 `inc-<32位hex>`。
- `severity`：`info` | `warning` | `critical`。
- `status`：见下方状态机。
- `createdAt` / `updatedAt`：RFC3339Nano，UTC。
- `summary`、`namespace`、`resourceKind`、`resourceName`：非空字符串。

### 状态机

Incident 状态迁移是严格白名单，非允许迁移返回 `INVALID_INCIDENT_REQUEST`（HTTP 400）。所有非终态均可进入 `failed`；`resolved`、`rejected`、`failed` 为终态，无出边；自环（from == to）恒为非法。

| 状态 | 允许的出边 |
| --- | --- |
| `received` | `triaging`, `failed` |
| `triaging` | `collecting`, `failed` |
| `collecting` | `analyzing`, `failed` |
| `analyzing` | `proposing`, `failed` |
| `proposing` | `awaiting_approval`, `rejected`, `failed` |
| `awaiting_approval` | `approved`, `rejected`, `failed` |
| `approved` | `executing`, `failed` |
| `executing` | `resolved`, `failed` |
| `resolved` / `rejected` / `failed` | （终态） |

创建 Incident 会自动触发五阶段诊断工作流：`triage → collector → root_cause → remediation → risk_review`，状态随之推进 `received → triaging → collecting → analyzing → proposing → awaiting_approval`。每个角色记录一条 `AgentRun`；角色输出校验失败或模型不可用时 Incident 进入 `failed` 终态且**不会执行任何动作**。模型未配置（`KUBEJOJO_LLM_BASE_URL` 为空）时仅运行确定性的 triage/collector，根因及后续角色记录为 `skipped` / `model_unavailable`，Incident 停在 `collecting`。

每个 AgentRun 保存 role、attempt、status、summary、输出 JSON、模型、Token 用量、起止时间与错误。每步开始/结束都会先持久化再广播一条 Incident 事件（`run_started` / `run_completed` / `incident_updated`），供 SSE 消费。

## 创建 Incident

`POST /api/v1/aiops/incidents`

请求体：

```json
{
  "summary": "Pod crash loop",
  "severity": "critical",
  "namespace": "default",
  "resourceKind": "Pod",
  "resourceName": "api-0"
}
```

- 成功：HTTP 201，`data` 为完整 Incident。创建后同步运行诊断工作流，返回的 `data` 是**工作流结束后的最新 Incident 状态**（有模型时通常为 `awaiting_approval`，无模型时为 `collecting`，角色输出非法时为 `failed`）。
- 失败：
  - 请求体不是合法 JSON、或字段类型不匹配 → 400 `INVALID_INCIDENT_REQUEST`。
  - `summary` 为空、`severity` 非法、`namespace` / `resourceKind` / `resourceName` 任一为空 → 400 `INVALID_INCIDENT_REQUEST`。

## 查询 Incident 证据

`GET /api/v1/aiops/incidents/:id/evidence`

- 需要有效 Session（任意角色）。
- 成功：HTTP 200，`data` 为 `{nodes, edges}`。`nodes` 是证据 DAG 的顶点（`kind` 取值 `snapshot` / `event` / `log` / `metric` / `agent` / `system`），`edges` 是支持关系边。Pod 证据由共享 client-go 采集：资源快照、关联事件、`TailLines=200` 尾部日志（单条 4 KiB 封顶）、Pod Metrics（可选能力）。
- 失败：ID 不存在 → 404 `INCIDENT_NOT_FOUND`。
- 安全：证据已脱敏——Pod 快照不含 env 值 / Secret 引用 / Token 路径；日志与事件消息中的疑似密钥行以 `[REDACTED]` 掩码。

## 查询诊断记录

`GET /api/v1/aiops/incidents/:id/runs`

- 需要有效 Session（任意角色）。
- 成功：HTTP 200，`data` 为 `{runs: [...]}`，每个 `run` 是 `{id, incidentId, role, attempt, status, summary, output, model, promptTokens, completionTokens, totalTokens, error, startedAt, completedAt}`。
- 失败：ID 不存在 → 404 `INCIDENT_NOT_FOUND`。

## 重新诊断

`POST /api/v1/aiops/incidents/:id/reanalyze`

- 需要 `operator` / `admin` 角色（viewer → 403 `FORBIDDEN`，未登录 → 401 `UNAUTHORIZED`）。
- 仅允许 `failed` / `rejected` / `resolved` 终态；其他状态 → 400 `REANALYZE_NOT_ALLOWED`。
- 语义：清空该 Incident 的既有证据，状态重置为 `received`，从头重跑五阶段工作流；历史 AgentRun 保留（新 run 的 `attempt` 递增）。
- 成功：HTTP 200，`data` 为工作流结束后的最新 Incident。
- 失败：ID 不存在 → 404 `INCIDENT_NOT_FOUND`；同一 Incident 并发触发 → 409 `WORKFLOW_BUSY`。

## Incident 事件流（SSE）

`GET /api/v1/aiops/incidents/:id/events?lastEventId=<int>`

- 需要有效 Session（任意角色）。
- 返回 `text/event-stream`。先重放 `id > lastEventId` 的已持久化事件（供断线恢复），再订阅实时事件；每 15 秒发送一条 `: heartbeat` 心跳，客户端断开即释放订阅。
- 每个事件帧：`id: <单调递增>`, `event: run_started|run_completed|incident_updated|reanalyze_started`, `data: <JSON>`。
- 慢客户端不阻塞工作流：事件**先持久化再广播**，订阅缓冲满时丢弃实时帧，历史仍可经 `lastEventId` 重放。

## 通用错误码

| HTTP | code | 说明 |
| --- | --- | --- |
| 400 | `INVALID_INCIDENT_REQUEST` | 请求体格式不正确，或业务参数校验失败（见下方各接口） |
| 400 | `REANALYZE_NOT_ALLOWED` | Incident 不在 `failed` / `rejected` / `resolved`，不可重新诊断 |
| 403 | `FORBIDDEN` | 当前角色无权执行该操作（如 viewer 调用 reanalyze） |
| 404 | `INCIDENT_NOT_FOUND` | 指定 ID 的 Incident 不存在 |
| 409 | `WORKFLOW_BUSY` | 同一 Incident 已有诊断在运行 |
| 500 | `CREATE_INCIDENT_FAILED` | 创建失败（不向客户端泄漏内部错误） |
| 500 | `LIST_INCIDENTS_FAILED` | 列表查询失败 |
| 500 | `GET_INCIDENT_FAILED` | 单条查询失败 |
| 500 | `GET_EVIDENCE_FAILED` | 证据查询失败 |
| 500 | `GET_RUNS_FAILED` | 诊断记录查询失败 |
| 500 | `REANALYZE_FAILED` | 重新诊断失败 |

## 查询 Incident 列表

`GET /api/v1/aiops/incidents`

- 成功：HTTP 200，`data` 为 Incident 数组，按 `updated_at DESC` 排序；无数据时返回 `[]`。
- 当前实现未开放查询过滤参数；`IncidentFilter`（status / namespace）已在 Repository 层支持，后续计划在 HTTP 层透出。

## 查询单个 Incident

`GET /api/v1/aiops/incidents/:id`

- 成功：HTTP 200，`data` 为完整 Incident。
- 失败：ID 不存在 → 404 `INCIDENT_NOT_FOUND`。

## 与 qd 旧接口的对应

| kubejojo（Go） | qd（Node，只读参考） |
| --- | --- |
| `POST /api/v1/aiops/incidents` | `POST /api/aiops/webhooks/alerts`（告警进入） |
| `GET /api/v1/aiops/incidents` | `GET /api/aiops/incidents` |
| `GET /api/v1/aiops/incidents/:id` | `GET /api/aiops/incidents/:id` |

其余 qd 接口的迁移状态见 `docs/aiops/migration-matrix.md`。
