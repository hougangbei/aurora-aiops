# Aurora AIOps 品牌迁移指南

本指南用于将已安装的 `kubejojo` 迁移到 `Aurora AIOps`。新版本默认使用 `aurora-aiops` 运行标识和 `AURORA_AIOPS_*` 环境变量，并在一个发布版本内兼容旧变量、数据库路径、会话 Cookie 和浏览器状态。

## systemd 迁移

1. 确认旧安装位于 `/opt/kubejojo`，并备份 `/opt/kubejojo/data/kubejojo.db`。
2. 先执行 `sudo ./scripts/migrate-kubejojo-to-aurora-aiops.sh --dry-run`。
3. 确认输出后执行 `sudo ./scripts/migrate-kubejojo-to-aurora-aiops.sh --apply`。
4. 验证 `systemctl status aurora-aiops` 和 `/healthz`，再验证登录、集群概览与 Incident 历史。
5. 如验证失败，执行 `sudo ./scripts/migrate-kubejojo-to-aurora-aiops.sh --rollback`。回滚不删除新文件，而是保存为 `/opt/aurora-aiops.rollback-<timestamp>`。

脚本复制数据而不移动旧安装。迁移稳定一个发布周期后，再人工清理旧目录、用户和 unit。

## 环境变量

- 将所有 `KUBEJOJO_*` 名称替换为同后缀的 `AURORA_AIOPS_*`。
- 新旧变量同时存在时，新变量优先。
- 使用旧变量时后端只记录变量名弃用警告，不记录变量值。
- 本兼容层将在下一个主版本删除。

## Kubernetes 迁移

Kubernetes Namespace、PVC 和 Secret 不能原地改名，不应直接删除旧资源。

1. 导出旧 Secret 的配置值，并对 SQLite 数据库做一致性备份。
2. 将旧 Deployment 缩容为 0，避免复制期间继续写入数据库。
3. 应用 `deploy/kubernetes/` 中的新清单，创建 `aurora-aiops` Namespace、Secret 和 PVC。
4. 通过两个临时 BusyBox Pod 与 `kubectl cp` 将旧 PVC 的 `kubejojo.db` 复制到新 PVC 的 `aurora-aiops.db`。
5. 启动新 Deployment，验证用户、Incident、审计记录、集群连接和修复审批。
6. 保留旧 Namespace 至少一个发布周期；删除前再次确认备份可恢复。

## GitHub 与在线更新

本地验证完成后，在 GitHub 将仓库改名为 `hougangbei/aurora-aiops`，然后更新本地 origin。在首个 Aurora AIOps Release 发布前，不要启用新的在线更新入口。
