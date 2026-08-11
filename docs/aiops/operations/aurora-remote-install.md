# Aurora AIOps 远程安装运维指南

本指南适用于 Phase-3 Aurora AIOps 项目安装器。安装任务由持久化 deployment worker 执行；浏览器请求只能选择服务器、精确版本和受约束的 bootstrap 配置，不能提供 URL、路径、systemd 单元或 shell 命令。

## 前置条件与配置

- 控制面使用 `AURORA_AIOPS_UPDATE_REPOSITORY` 指定 GitHub Releases 仓库；默认和本文示例均为 `hougangbei/aurora-aiops`。可选的 `AURORA_AIOPS_UPDATE_GITHUB_TOKEN` 仅用于 GitHub API 访问，不能写入日志，也不会发送给 release CDN。
- 目标必须是 Linux `amd64` 或 `arm64`，运行 systemd，并存在健康的 `/etc/kubernetes/admin.conf`。目标应能通过已确认主机指纹的 SSH 连接访问。
- 目标用户必须为 `root`，或具备非交互 `sudo -n` 权限；安装器拒绝需要交互输入的 sudo。
- 目标至少应有 2 GiB 可用磁盘空间。安装器创建锁定的 `aurora-aiops` 系统用户和组，并使用其身份运行服务。
- 任务配置只接受 `bootstrapAdminUser` 与 `bootstrapAdminPassword`。用户名为 3–64 个小写 DNS-label 字符，密码为 12–128 个非控制字符。规范化后的 JSON 使用 `AURORA_AIOPS_ASSET_ENCRYPTION_KEY` 加密保存；未配置该密钥时安装写入被禁用。

## 固定安装步骤

任务按以下顺序执行，步骤 ID、百分比和路径不可由请求修改：

1. `verify-ssh`（10%）：执行固定 locale 约束命令并确认已知主机指纹。
2. `preflight`（20%）：检查 Linux、systemd、Kubernetes admin 配置、架构、磁盘和权限。
3. `resolve-release`（30%）：请求精确 `v<version>` tag，选择同架构 Linux archive，并解析唯一的 checksum 条目。
4. `transfer-release`（40%）：控制面以 0600 临时文件下载并校验大小/摘要，之后通过受限 SSH 上传到目标 staging 目录；完成或失败后删除控制面临时文件。
5. `verify-checksum`（50%）：目标使用固定 SHA-256 命令核对已上传 archive。
6. `activate-version`（65%）：先列出并检查 tar 成员，再解包到新版本目录，通过临时 symlink 原子切换 `current`。
7. `configure-service`（78%）：写入环境文件、服务单元和 kubeconfig，设置所有权与权限。
8. `restart-service`（90%）：daemon reload 后 enable 并启动服务。
9. `verify-health`（100%）：确认 systemd active/enabled，并访问本机 `/api/v1/system/version` 验证选定版本。

步骤 probe 命中时跳过已满足的动作，保证 retry 幂等。任务、事件和错误消息不保存 bootstrap 凭据、SSH 凭据、环境文件内容或 kubeconfig。

## 目标路径与 systemd

安装器只允许以下路径族：

- `/tmp/aurora-aiops/<task-id>/`：上传 archive 和一次性 bootstrap 环境文件；
- `/opt/aurora-aiops/releases/<version>/`：已验证的版本目录；
- `/opt/aurora-aiops/current`：指向当前版本的 symlink；
- `/opt/aurora-aiops/config/kubeconfig`：由 `/etc/kubernetes/admin.conf` 复制而来，归属 `aurora-aiops:aurora-aiops`、模式 0600；
- `/etc/aurora-aiops/aurora-aiops.env`：服务配置，归属服务用户、模式 0600；
- `/etc/systemd/system/aurora-aiops.service`：固定服务单元。

服务工作目录为 `/opt/aurora-aiops/current`，可执行文件为 `/opt/aurora-aiops/current/aurora-aiops`，并读取可选的 `/etc/aurora-aiops/aurora-aiops.env`。服务由 systemd 以 `aurora-aiops` 用户运行；安装器只允许 root 或 `sudo -n` 执行特权动作。

## Bootstrap 账号生命周期

Bootstrap 用户名/密码只在首次配置阶段通过受控 stdin 写入环境文件，并在日志、事件、API 响应、命令记录和错误中脱敏。首次健康启动完成账号创建后，安装器移除 bootstrap 值并重启一次；加密任务配置只供安全 retry 使用，不能通过 API 读出。

## 失败、取消、重试与回滚

- 任务失败或取消不会自动撤销已发生的远端副作用。先查看事件和目标实际状态，再由管理员调用 retry；retry 创建新任务并保留 `retryOf`，依靠 probe 跳过已正确步骤。
- 激活完成前失败时，安装器保留原 `current` symlink 和服务。重启或健康检查阶段失败时，先修复目标依赖，再 retry；不要直接编辑 SQLite 状态。
- 旧版本目录保留在 `/opt/aurora-aiops/releases/<version>/`，由运维按备份和磁盘策略清理。手工回滚时，确认目标版本目录存在且已通过校验，然后将 `current` 改为对应版本目录，执行 `systemctl daemon-reload`、`systemctl restart aurora-aiops.service`，最后重新检查本机版本和健康接口。回滚操作应记录在审计系统中。

## 运行检查

1. 在项目中心确认任务为 `succeeded`，并检查九个步骤事件按 ID 顺序完成。
2. `GET /api/v1/deployment-tasks/:taskID` 核对任务/步骤状态；SSE 断线后使用 `Last-Event-ID` 或 `lastEventId` 重放。
3. 在目标执行 `systemctl is-active aurora-aiops.service` 和 `systemctl is-enabled aurora-aiops.service`，再从控制面读取 `/api/v1/system/version`。
4. 若目标不可用、release tag/checksum 不匹配或加密密钥缺失，保持任务失败并修复依赖；不要把失败记录改写为成功。

真实主机安装必须使用明确标记为 disposable 的 Ubuntu 22.04/24.04 或 Debian 12 目标，并单独记录版本、架构、archive 摘要、任务 ID、事件序列和清理决定；不要在文档中记录任何凭据或环境文件原文。
