# AIOps API v1

本页记录 Aurora AIOps Go 后端已实现的 `/api/v1` 接口。当前阶段已落地 Incident 的创建与查询、平台账号登录、共享集群连接状态与节点发现、证据链与多智能体诊断工作流（Evidence / AgentRun / Reanalyze / SSE 事件流）、资产盘点以及 Phase-2 持久化部署任务进度接口。具体项目安装器与真实目标机执行适配器仍在后续 Phase-3/4 实现。

所有接口返回统一信封 `{code, message, data}`：

- 成功：`code = "OK"`，`message = "success"`，`data` 为资源或资源数组（空列表必须是 `[]`，不会是 `null`）。
- 失败：`code` 为业务错误码，`message` 为可读错误信息，`data` 省略。

## 认证入口

平台使用账号密码 + 服务端 Session。`POST /api/v1/auth/login` 是 `/api/v1` 下唯一匿名入口，登录成功后通过 `Set-Cookie: aurora-aiops_session`（HttpOnly + SameSite=Lax）建立会话。其余接口均需携带该 Cookie；后端通过共享 kubeconfig 访问集群，**不再接收任何请求级 Kubernetes Token**。前端 Axios 使用 `withCredentials: true` 同源携带 Cookie。

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

创建 Incident 会自动触发五阶段诊断工作流：`triage → collector → root_cause → remediation → risk_review`，状态随之推进 `received → triaging → collecting → analyzing → proposing → awaiting_approval`。每个角色记录一条 `AgentRun`；角色输出校验失败或模型不可用时 Incident 进入 `failed` 终态且**不会执行任何动作**。模型未配置（`AURORA_AIOPS_LLM_BASE_URL` 为空）时仅运行确定性的 triage/collector，根因及后续角色记录为 `skipped` / `model_unavailable`，Incident 停在 `collecting`。

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

## 资产服务器 API

资产盘点服务面向 Linux 目标机，采用免 Agent 的 SSH 采集方式。开发与安全边界见 [资产盘点后端基础开发参考](../architecture/asset-inventory-development.md)。以下 10 个端点均已实现，均要求有效 Session，并使用本页开头定义的统一信封 `{code, message, data}`。

| 方法 | 路径 | Phase-1 RBAC | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/assets/servers` | viewer / operator / admin | 服务器列表；空列表的 `data` 为 `[]`。 |
| `POST` | `/api/v1/assets/servers` | admin | 新增服务器。 |
| `GET` | `/api/v1/assets/servers/:id` | viewer / operator / admin | 查询单台服务器。 |
| `PATCH` | `/api/v1/assets/servers/:id` | admin | 更新连接信息或凭据。 |
| `DELETE` | `/api/v1/assets/servers/:id` | admin | 删除服务器，成功 `data` 为 `{ "deleted": true }`。 |
| `POST` | `/api/v1/assets/servers/:id/test-connection` | operator / admin | 测试 SSH 连接和主机密钥。 |
| `POST` | `/api/v1/assets/servers/:id/confirm-host-key` | admin | 显式确认探测到的主机密钥。 |
| `POST` | `/api/v1/assets/servers/:id/collect` | operator / admin | 立即采集最新资产快照。 |
| `GET` | `/api/v1/assets/servers/:id/snapshots/latest` | viewer / operator / admin | 查询最近一次成功快照。 |
| `GET` | `/api/v1/assets/servers/:id/software` | viewer / operator / admin | 查询最近一次成功快照的软件列表；空列表为 `[]`。 |

### 请求、响应与 SSH 行为

- 创建请求使用 `name`、`address`、`username`、可选 `sshPort`、`credentialAuthType`（`password` 或 `private_key`）以及对应认证字段。凭据字段 `password`、`privateKey`、`passphrase` **只**在 `POST` / `PATCH` 写入请求中接收；省略 `sshPort` 或传入 `0` 时默认为 `22`。
- 所有服务器响应仅暴露凭据元数据 `credentialAuthType` 与 `credentialConfigured`。响应绝不返回凭据明文、`credentialId`、`nonce`、`ciphertext`、`privateKey` 或 `passphrase`；主机密钥冲突中附带的 `server` 也遵守相同脱敏规则。
- `POST /api/v1/assets/servers` 成功时返回 HTTP 201。若未要求 `testConnection`，服务器以 `pending` 状态创建，因此离线或暂不可达目标机可以先登记；随后可由有权限的操作者显式测试或采集。
- 首次观察到未知主机密钥、或已确认主机密钥发生变化时，连接测试、采集或带 `testConnection` 的创建返回 HTTP 409 `SSH_HOST_KEY_CONFIRMATION_REQUIRED`。`data` 含观察到的 `fingerprint` 和已脱敏的 `server`（连接测试还含 `trusted`、`changed`）；管理员必须调用 confirm-host-key 并提交相同 `fingerprint` 后才会信任该密钥。

### 主要错误码

资产接口稳定使用以下错误码；未登录和越权仍分别为通用的 401 `UNAUTHORIZED` 与 403 `FORBIDDEN`。

| HTTP | code | 说明 |
| --- | --- | --- |
| 400 | `INVALID_ARGUMENT` | JSON、字段或凭据校验失败。 |
| 404 | `ASSET_NOT_FOUND` | 服务器或其最新快照不存在。 |
| 409 | `ASSET_NAME_CONFLICT` | 服务器名称已存在。 |
| 409 | `SSH_HOST_KEY_CONFIRMATION_REQUIRED` | 主机密钥未知或已变化，需显式确认。 |
| 409 | `ASSET_ACTIVE_TASK` | 服务器存在活动部署任务，不能执行受限操作。 |
| 503 | `ASSET_ENCRYPTION_UNAVAILABLE` | 未配置或无法使用资产凭据加密。 |
| 500 | `ASSET_INTERNAL_ERROR` | 未分类的资产操作失败，不泄漏内部错误或凭据。 |

`project_installations` 仅存在于存储 schema，独立的 installation 记录写入仍属于后续项目安装器；Phase-2 已提供基于成功 install 任务的 installations 查询和 deployment task API（见下节）。Phase-1 也没有定时采集 scheduler，使用 `AURORA_AIOPS_ASSET_COLLECT_INTERVAL` 不会自动创建采集任务。

## 部署项目与持久化任务 API（Phase-2）

Phase-2 将部署任务从资产详情页的临时状态提升为 SQLite 中的持久化任务、步骤和事件。任务只接受已注册项目安装器生成的固定步骤计划；HTTP 请求不能注入 shell 命令、路径或远端 URL。当前默认服务只装配空项目目录和不可用的目标执行适配器，因此本节描述的是稳定 API/状态机契约，真实 Aurora/Kubernetes 安装器分别在后续 Phase-3/4 注册。

### 项目目录

- `GET /api/v1/projects`：需要登录（viewer / operator / admin），返回已注册项目数组；无项目时 `data` 为 `[]`。
- `GET /api/v1/projects/:projectID`：需要登录，返回项目描述、支持版本、操作系统族和架构；未知项目返回 404 `DEPLOYMENT_UNKNOWN_PROJECT`。

### 创建与查询任务

- `POST /api/v1/projects/:projectID/install`：仅 admin。请求体为 `{ "serverId": "...", "version": "...", "configuration": {} }`；配置先由对应安装器严格规范化，再以 AES-256-GCM 加密写入任务，原始请求字节不会保留。成功返回 201 和任务 DTO，初始状态为 `queued`。
- `GET /api/v1/assets/servers/:serverID/tasks`：需要登录，按创建时间升序返回该服务器的任务历史。
- `GET /api/v1/assets/servers/:serverID/installations`：需要登录，仅返回该服务器已成功的 `install` 任务；Phase-2 不创建 `project_installations` 记录。
- `GET /api/v1/deployment-tasks/:taskID`：需要登录，返回 `{ "task": {...}, "steps": [...] }`。任务/步骤 DTO 不含加密配置、凭据、任务值或租约内部字段。

任务状态固定为 `queued`、`running`、`succeeded`、`failed`、`cancelled`；步骤状态为 `pending`、`running`、`succeeded`、`failed`、`skipped`、`cancelled`。百分比只能单调递增，成功必须以所有步骤完成且达到 100% 结束；终态不可再次变更。每台服务器通过 SQLite partial unique index 最多有一个 `queued` 或 `running` 任务。

### 取消与重试

- `POST /api/v1/deployment-tasks/:taskID/cancel`：仅 admin；`queued`/`running` 任务设置 `cancelRequested`，由 worker 在步骤边界或可取消远程命令中止并最终标记 `cancelled`。取消**不会回滚已完成的远程改变**，重试必须依靠安装器的 probe/idempotency 检查。
- `POST /api/v1/deployment-tasks/:taskID/retry`：仅 admin；只允许从 `succeeded`、`failed`、`cancelled` 创建新任务。新任务有新的 ID，`retryOf` 指向原任务，并重新经过同一台服务器的活动任务互斥检查。

### 事件流与断线恢复

`GET /api/v1/deployment-tasks/:taskID/events` 需要登录（viewer / operator / admin），返回 `text/event-stream`。客户端可使用 `Last-Event-ID`，或在无法设置请求头时使用 `?lastEventId=<非负整数>`；服务端先从 SQLite 重放 `id > lastEventId` 的事件，再订阅实时唤醒。事件帧包含 `id`、`event`、JSON `data`，每 10 秒发送 `: heartbeat`。事件先提交到 SQLite 再唤醒订阅者，慢客户端不会阻塞写入；终态任务重放完毕后连接关闭。前端若连续 3 次 SSE 失败，会保留最后事件 ID 并切换为每 2 秒查询任务详情，直到终态；详情刷新时百分比不会倒退。

### 部署错误码与权限

| HTTP | code | 说明 |
| --- | --- | --- |
| 400 | `INVALID_ARGUMENT` | 请求 JSON、版本、配置或参数不合法。 |
| 401 | `UNAUTHORIZED` | 未登录。 |
| 403 | `FORBIDDEN` | viewer/operator 调用 admin-only 的安装、取消或重试。 |
| 404 | `NOT_FOUND` | 任务、服务器或资源不存在。 |
| 404 | `DEPLOYMENT_UNKNOWN_PROJECT` | 项目未注册。 |
| 409 | `DEPLOYMENT_ACTIVE_TASK` | 同一服务器已有 `queued`/`running` 任务。 |
| 409 | `DEPLOYMENT_INVALID_TRANSITION` | 状态、步骤或重试边界不允许该操作。 |
| 422 | `DEPLOYMENT_UNSUPPORTED_TARGET` | 目标 OS/架构不在项目目录支持矩阵。 |
| 503 | `DEPLOYMENT_ENCRYPTION_UNAVAILABLE` | 未配置可用的 32 字节资产加密主密钥。 |

完整的租约恢复、事件保留和人工处置流程见 [部署任务恢复运维指南](operations/deployment-task-recovery.md)。

## 与 qd 旧接口的对应

| aurora-aiops（Go） | qd（Node，只读参考） |
| --- | --- |
| `POST /api/v1/aiops/incidents` | `POST /api/aiops/webhooks/alerts`（告警进入） |
| `GET /api/v1/aiops/incidents` | `GET /api/aiops/incidents` |
| `GET /api/v1/aiops/incidents/:id` | `GET /api/aiops/incidents/:id` |

其余 qd 接口的迁移状态见 `docs/aiops/migration-matrix.md`。
