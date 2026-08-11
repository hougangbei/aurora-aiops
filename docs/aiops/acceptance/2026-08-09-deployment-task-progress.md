# Phase-2 部署任务与进度验收记录

**验收日期：** 2026-08-11
**范围：** durable deployment task schema/repository、租约恢复、事件持久化与 SSE、脱敏日志、worker 状态机、任务 API/RBAC、前端进度恢复/轮询降级，以及可安装 release 资产契约。本文不把占位目标适配器或 loopback 测试推断为真实主机安装验收。

## 已验证范围与明确限制

- SQLite 持久化 `deployment_tasks`、`deployment_steps`、`deployment_events` 和 `deployment_task_values`；同一服务器最多一个 `queued`/`running` 任务，任务终态不可变，步骤百分比单调递增。
- 事件在提交后唤醒订阅者，可从 SQLite 重启重放；SSE 支持 `Last-Event-ID` 与 `lastEventId`，服务端 10 秒 heartbeat。前端连续 3 次连接失败后改用 2 秒轮询，并在终态做最终刷新。
- worker 覆盖 probe 跳过、失败/超时、取消、panic、租约续期、过期恢复、旧 worker fencing 和脱敏日志；取消不回滚已发生的远端副作用，retry 通过 `retryOf` 创建新任务。
- 当前 `server.Run` 仍使用空项目目录和明确返回 `target execution unavailable` 的占位目标执行适配器；未执行真实远端 SSH、Aurora 安装或 kubeadm 安装。对应安装器属于 Phase-3/4。
- 本轮只做 loopback/进程内和 SQLite 验证，没有连接生产集群或真实主机。

## 命令证据

以下命令在当前工作区执行；退出码为 0 的命令标为 PASS。Go 全量测试包含后续 release resolver 测试，resolver 文件落地后重新执行并通过。

| 命令 | 结果 |
| --- | --- |
| `cd server && go test -race ./internal/deployment ./internal/server` | PASS；deployment 包 `3.233s`，server 包随后完成并通过（server race 测试包含 SSE/RBAC 路由）。 |
| `cd server && go test ./... -count=1` | PASS；所有 Go 包通过，deployment 包包含 repository/event/worker/service/release resolver 测试。 |
| `cd server && go vet ./...` | PASS；无诊断输出。 |
| `cd web && npm test` | PASS；`30` files、`94` tests。stderr 只有既有 jsdom `getComputedStyle()` pseudo-element 未实现提示。 |
| `cd web && npm run build` | PASS；Vite 构建成功，主 JS `4,797.56 kB`（gzip `1,408.22 kB`），保留既有 `>500 kB` chunk warning。 |
| `bash scripts/verify-release-assets.test.sh` | PASS；包含重复归档成员、错误 checksum、缺失归档等负向断言，最终输出 `Release asset contract tests passed`。 |
| `git diff --check` | PASS；无空白错误。 |
| 生产源码 secret scan（排除测试 fixture） | PASS；未发现 PEM、常见 API key 或带引号的 password/passphrase/token 明文。 |

计划中的宽匹配 grep 会命中已有规划示例、UI 的 `token` 配置字段及测试样例（并非凭据），因此另行使用上述按 secret 形态约束的生产源码扫描；不将这些字段名误报为泄漏。

## 持久化/恢复证据

`go test ./... -count=1` 覆盖以下可重复测试：

- `TestEventStoreReplaysAfterSQLiteReopen`：关闭并重新打开 SQLite 后，`ListAfter` 仍按事件 ID 顺序重放。
- `TestRepositoryFencesExpiredLeaseAndRestartsRunningStep`：过期租约回收后，未完成步骤回到 `pending`，旧 owner 不能继续写入。
- `TestWorkerRecoversExpiredClaimAndResumes`：worker 启动先恢复过期任务，再领取并从未完成步骤继续。
- `TestRepositoryNotifierRunsAfterCommit` 与 `TestEventStoreSlowSubscriberDoesNotBlockAndUnsubscribeRemovesIt`：提交先于唤醒，慢订阅者不会阻塞 SQLite 写入，取消订阅可移除。
- `useDeploymentEvents.test.tsx`：事件 ID 去重/乱序忽略、单调百分比、重连与三次失败后的 polling fallback、终态最终 fetch。

## RBAC 与安全边界

viewer/operator/admin 均可读取项目、任务、步骤、安装历史和 SSE；仅 admin 可 install、cancel、retry。任务/事件/API 不返回配置密文、SSH 凭据、租约内部字段或持久化 task values；worker 日志在写入前脱敏并限制 16 KiB。未配置 `AURORA_AIOPS_ASSET_ENCRYPTION_KEY` 时部署写入被禁用，资产读取不受影响。

## 后续验收门槛

Phase-3 Aurora 安装和 Phase-4 kubeadm 安装需在明确标记为 disposable 的目标机上另行验收，记录 release checksum、远端版本/健康检查、浏览器刷新重放、取消边界和幂等 retry；没有目标机时必须保持“未运行”，不能以本文件的状态机测试替代端到端安装验收。
