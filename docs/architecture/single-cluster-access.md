# 单集群接入与平台账号认证

> 基线日期：2026-08-01。对应计划 01A。

本页描述 Aurora AIOps 如何用平台账号密码登录，并通过「显式 kubeconfig / in-cluster ServiceAccount 身份 / 默认 kubeconfig」的确定性顺序稳定访问单个 Kubernetes 集群。平台身份与 Kubernetes 身份彻底分离。

## 连接链路

```
平台账号密码
   │ POST /api/v1/auth/login
   ▼
auth.Service（bcrypt 校验 + 生成 32 字节随机 Session）
   │ Set-Cookie: aurora-aiops_session（HttpOnly + SameSite=Lax）
   ▼
Gin 鉴权 RequireSession（按 Cookie 里的 Session 摘要查 SQLite）
   ▼
共享 ClusterService（进程启动时由 kube.NewSharedClient 创建一次，全请求复用）
   ▼
Kubernetes API Server（client-go，使用显式 kubeconfig 或 in-cluster ServiceAccount 身份）
   ▼
Node API → Node InternalIP（来自 Node.Status.Addresses，非扫描结果）
```

要点：

- 用户在登录时只提交平台账号密码；**后端从不读取、也不接收用户的 Kubernetes Token**。
- 进程启动时按 `AURORA_AIOPS_KUBECONFIG` → `KUBECONFIG` → 集群内 ServiceAccount 身份 → `~/.kube/config` 的顺序选择集群身份，构建唯一共享 client-go 客户端。运行在 Pod 内时优先使用 in-cluster 身份，**不挂载 kubeconfig**。请求级不再覆盖 Token。
- `aurora-aiops_session` Cookie 只存随机 Session 值的**摘要**，原始值仅经 HttpOnly Cookie 传输一次。
- 节点发现只调用 Kubernetes Node API。`internalAddress` 取自 Node 状态的 `NodeInternalIP`（IPv4 优先），**后端不会连接该 IP**：不 ping、不端口扫描、不 SSH。
- Metrics API 不可用只会把连接状态降级为 `degraded`，不会误判整个集群离线。

## 认证与角色

- `POST /api/v1/auth/login` 是 `/api/v1` 下唯一匿名入口；`/healthz` 同样匿名。
- `GET /api/v1/auth/me`、`POST /api/v1/auth/logout` 需要有效 Session。
- 角色：`admin` / `operator` / `viewer`。`viewer` 只读；`operator` 可创建 / reanalyze / reject / approve Incident 和执行部分操作；`admin` 额外可 execute / rollback 与修改平台设置。
- 审计 actor 来自 `ActorFromContext` 得到的平台用户，与共享 Kubernetes 身份可区分。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `AURORA_AIOPS_KUBECONFIG` | 显式指定共享 kubeconfig 路径（最高优先级） |
| `KUBECONFIG` | 未设 `AURORA_AIOPS_KUBECONFIG` 时取第一个路径 |
| 集群内 ServiceAccount | 前两个变量都为空且在 Pod 内运行时使用（优先级高于 `~/.kube/config`） |
| `AURORA_AIOPS_KUBE_TIMEOUT` | client-go 请求超时，默认 `10s`，非法值阻止启动 |
| `AURORA_AIOPS_KUBE_QPS` | 客户端限速，默认 `20` |
| `AURORA_AIOPS_KUBE_BURST` | 突发限速，默认 `40` |
| `AURORA_AIOPS_BOOTSTRAP_ADMIN_USER` | users 表为空时创建首个 admin 的用户名 |
| `AURORA_AIOPS_BOOTSTRAP_ADMIN_PASSWORD` | 同上，密码；任一缺失且表为空则启动失败 |

## 连接状态 API

- `GET /api/v1/cluster/connection`：返回最近一次探测缓存（最长 30 秒）。`connected` / `degraded` 返回 200；`unreachable` 返回 503；权限不足返回 `CLUSTER_PERMISSION_DENIED`。
- `POST /api/v1/cluster/connection/test`：立即探测，需 `operator` / `admin`；并发探测合并为一次。
- `GET /api/v1/nodes`：返回节点数组，每项为既有富 `NodeItem` 并携带 `internalAddress` 与 `hostname`（`ip`/`internalAddress` 来自共享 `cluster.SelectNodeAddress`），不执行任何主机命令。

## 明确不做

- 不扫描 RFC1918 网段、不用 ping/ARP 判定机器、不根据 `10.x`/`172.16/12`/`192.168/16` 自动建立 SSH。
- 不保存或接收用户 Kubernetes Token；不做多集群、不做每用户独立 kubeconfig、不引入节点 Agent。
- 不把 Metrics API 不可用误判为整个集群离线。
