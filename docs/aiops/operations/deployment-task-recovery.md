# 部署任务恢复与运维指南

本指南适用于 Phase-2 持久化部署任务。SQLite 是任务、步骤和事件的权威状态源；内存订阅只负责唤醒当前连接的 SSE 客户端，进程重启不会丢失已提交的进度。项目安装器必须提供固定、可重建的步骤计划，worker 不接受 HTTP 传入的命令或路径。

## 状态与互斥

任务状态只有 `queued`、`running`、`succeeded`、`failed`、`cancelled`。步骤状态为 `pending`、`running`、`succeeded`、`failed`、`skipped`、`cancelled`。步骤按 ordinal 顺序运行；probe 命中时跳过远程 run 但仍记录完成。任务百分比只增不减，成功任务必须所有步骤为 `succeeded`/`skipped` 且百分比为 100；终态不可再改变。

每台服务器通过 `idx_deployment_tasks_one_active_server` partial unique index 只允许一个 `queued` 或 `running` 任务。创建第二个活动任务返回 `409 DEPLOYMENT_ACTIVE_TASK`。查看历史使用服务器任务列表；不要通过重复提交来“唤醒” worker。

```text
queued --claim/lease--> running --all steps 100%--> succeeded
   |                         |                       ^
   | cancel                  | step/target failure  |
   v                         v                       |
cancelled                 failed <---- retry -------+
```

`retry` 不复用原任务行，而是创建新 ID 并在 `retryOf` 保存原 ID；原任务及其事件保持只读。重试前先检查远端实际状态，依靠安装器 probe 做幂等判断，不能假设取消或断电自动撤销远端操作。

## 进程重启、租约与恢复

worker 领取任务时写入 owner、过期时间和 `startedAt`。worker 续租周期约为租约时长的三分之一；所有步骤/终态写入都要求相同 owner 且租约仍未过期，旧 worker 不能在新 worker 接管后继续修改任务。

启动或每次轮询先执行 `RecoverExpired`：过期的 `running` 任务回到 `queued`，未完成的 `running` 步骤重置为 `pending`，已完成的步骤保留。随后按创建时间领取最早任务并从第一个未完成步骤继续。若任务已无可用项目安装器、配置无法解密或目标执行适配器不可用，worker 记录脱敏失败信息并结束为 `failed`；运维应修复依赖后使用 retry，不要手工改 SQLite 状态。

排障顺序：

1. `GET /api/v1/deployment-tasks/:taskID` 查看任务和步骤，确认是否为租约恢复后的 `queued`、终态或 `cancelRequested`。
2. 查看同一任务的 SSE 历史（见下一节），以最后事件 ID 为准，不以浏览器内存中的进度为准。
3. 检查 worker 日志中的错误码（例如 `TARGET_UNAVAILABLE`、`CONFIG_OPEN_FAILED`、`STEP_TIMEOUT`、`INSTALLER_PANIC`）；日志和事件应已脱敏。
4. 只有确认远端状态可安全重跑后，管理员才调用 retry。若仍有活动任务，先等待旧任务进入终态；不要删除任务行解除互斥。

## SSE 重放与轮询降级

连接 `GET /api/v1/deployment-tasks/:taskID/events` 时，优先发送 `Last-Event-ID`；原生 `EventSource` 无法设置请求头时使用 `?lastEventId=<id>`。服务端按 `id` 升序重放 SQLite 中严格大于该 ID 的事件，再注册实时订阅。事件提交成功后才发出内存唤醒，因此客户端可以安全地在断线或进程重启后重放；重复或乱序事件由前端忽略，任务详情中的百分比仍保持单调。

服务端每 10 秒发 heartbeat。前端连接失败会指数退避重连（上限 15 秒）；连续 3 次失败后关闭 SSE，改为每 2 秒拉取任务详情，并在任务进入终态后做一次最终刷新。轮询不是第二个状态源，只是读取 SQLite 的兜底方式。

## 取消边界与远端改变

取消请求只设置 `cancelRequested`，不会把 `running` 任务立即伪装成 `cancelled`。worker 在步骤边界、probe/run 的可取消 context 中观察标记，并最终写入 `cancelled`。已经上传文件、写入配置、启用服务或其他远端副作用不会自动回滚；取消后必须按项目安装器的探针检查远端状态，再决定 retry、人工修复或回滚。任何回滚动作都不由通用 Phase-2 worker 猜测执行。

## 数据保留、备份与秘密

- Phase-2 没有自动 TTL/归档作业；任务、步骤和事件在 SQLite 中保留，直到数据库备份策略或明确的运维清理。删除资产时外键级联删除其任务和事件，因此生产环境先备份数据库并确认审计要求。
- 配置使用复用的 `AURORA_AIOPS_ASSET_ENCRYPTION_KEY` 以 AES-256-GCM 加密；缺少主密钥时只禁用部署写入，不应禁用资产读取。不要轮换后丢弃旧密钥，旧任务 retry 仍需解密原配置。
- 任务/API DTO、SSE 事件和审计仅保存任务、服务器、项目、状态、错误码及脱敏消息，不返回 nonce、ciphertext、SSH 凭据、配置原文或 allow-listed task values。
- worker 日志在写入前屏蔽配置中的标量秘密、URL 编码值、`Authorization`、`password/passphrase/token` 字段及 PEM 内容，输出上限为 16 KiB。发现疑似泄漏时立即限制日志访问并按密钥轮换流程处置，不要把原文复制到 issue 或聊天记录。

## 权限与当前 Phase 限制

viewer/operator/admin 都可以读取项目、任务、步骤、安装历史和 SSE；只有 admin 可以创建 install、cancel 或 retry。所有路由仍受 Session/RBAC 保护，未登录为 401，越权为 403。

当前默认 Aurora 服务尚未注册任何项目安装器，且 server 装配的是明确返回“target execution unavailable”的占位目标适配器；因此本地可验收状态机、加密、恢复、SSE 和前端轮询，但不能声称已完成真实远端安装。Aurora release 安装器和 kubeadm 安装器将在后续 Phase-3/4 提供固定项目目录与目标执行能力。
