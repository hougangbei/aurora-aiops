# AIOps API v1

本页记录 kubejojo Go 后端已实现的 `/api/v1` 接口。当前阶段（计划 01 + 01A）落地了 Incident 的创建与查询、平台账号登录、共享集群连接状态与节点发现；审批、执行、工具调用、ChatOps、集成配置在后续计划中实现。

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

当前阶段状态流转逻辑已实现（`internal/aiops/state_machine.go`、`service.Advance`），但 HTTP 层暂未暴露状态迁移接口，由后续计划接入。

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

- 成功：HTTP 201，`data` 为完整 Incident（含服务端生成的 `id` 与初始 `status = "received"`）。
- 失败：
  - 请求体不是合法 JSON、或字段类型不匹配 → 400 `INVALID_INCIDENT_REQUEST`。
  - `summary` 为空、`severity` 非法、`namespace` / `resourceKind` / `resourceName` 任一为空 → 400 `INVALID_INCIDENT_REQUEST`。

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
