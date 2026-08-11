# Aurora AIOps 项目安装 Phase-3 验收记录

**验收日期：** 2026-08-11  
**范围：** Linux release asset contract、GitHub 精确版本 resolver、Aurora 固定项目目录/九步安装计划、持久化任务与前端项目中心的自动化验收。本文只记录实际执行的本地证据；不把 fake target、loopback 或占位适配器推断为真实主机安装。

## 结论与限制

- 自动化门禁通过：release archive 契约、Aurora catalog/unit/race、Go 全量测试与 vet、Web 全量测试与构建、品牌/仓库归属检查均通过。
- `AURORA_AIOPS_E2E_HOST` 在本次环境中未设置，因此没有连接真实 SSH 主机、下载真实发布包、执行 systemd/Kubernetes 安装或记录生产健康检查。没有伪造 task ID、archive 摘要或远端事件序列。
- 当前目标执行能力的自动化测试使用 command recorder/fake resolver；它验证固定命令、路径边界、checksum/幂等和凭据脱敏，但不能替代 disposable Linux 主机验收。
- release resolver 只接受配置的 `hougangbei/aurora-aiops` 仓库和精确 `v<version>` tag；本轮没有使用历史上游 owner、其 token 或其 release。

## Release asset contract

`bash scripts/verify-release-assets.test.sh`：**PASS**（exit 0）。测试覆盖合成 `linux_amd64`、`linux_arm64`、`darwin_arm64` archive 与 `checksums.txt`，并验证：

- Linux archive 名称为 `aurora-aiops_<version>_linux_<arch>.tar.gz`，包含版本化顶层目录、可执行 `aurora-aiops`、`aurora-aiops.service` 和迁移脚本；
- 每个归档恰有一个 checksum 条目，checksum 与实际 SHA-256 相符；
- 重复 archive 成员、重复/缺失条目、错误 checksum、意外归档名、绝对路径、`..` 路径、symlink 和缺失架构均失败；
- release 目录不包含可变 `latest` symlink。

本地输出包含预期的负向断言（duplicate archive member、unexpected archive name、checksum mismatch），最终为 `Release asset contract tests passed`。`shellcheck` 未安装（exit 127），因此未声称本地 shellcheck 通过；CI workflow 仍将其作为发布门禁。

## Go 与 resolver 证据

| 命令 | 结果 |
| --- | --- |
| `cd server && go test -race ./internal/deployment/...` | PASS；deployment 与 catalog 包通过。覆盖 GitHub resolver、Aurora catalog/commands、租约 fencing、事件、redactor 与 worker。 |
| `cd server && go test ./... -count=1` | PASS；所有 Go 包通过，包括 `internal/deployment/catalog` 和 `internal/server`。 |
| `cd server && go vet ./...` | PASS；无诊断输出。 |

Resolver 的 `httptest` 单元测试覆盖精确 tag、架构匹配、唯一 checksum、尺寸/摘要边界、重定向 host、token 不发送到 CDN、rate-limit 错误和 bounded download。测试使用本地 HTTP server，不产生外部 GitHub token 或真实 release 访问。

Aurora catalog 单元/竞态测试覆盖项目 metadata、Linux/`amd64|arm64` 支持矩阵、九个固定步骤、路径/命令注入防护、root/`sudo -n` 选择、checksum 失败停止解包、原子 `current` 切换、幂等 probe、bootstrap 脱敏和 resolver/target 错误 fencing。`go test -race ./internal/deployment/...` 已通过。

## Web 证据

| 命令 | 结果 |
| --- | --- |
| `cd web && npm test` | PASS；34 files、104 tests。包含项目列表/详情、安装 modal、目标筛选、凭据清理、RBAC 禁用和任务进度测试。stderr 只有既有 jsdom `getComputedStyle()` pseudo-element 提示。 |
| `cd web && npm run build` | PASS；Vite 构建成功；主 JS 约 4.81 MB，保留既有 `>500 kB` chunk warning。 |

## 品牌、归属与秘密扫描

- `./scripts/verify-brand-rename.sh`：**PASS**，输出 `Aurora AIOps brand guard passed` 与 `Repository ownership guard passed`。
- `git grep` 仓库归属扫描：应用代码、普通文档和脚本之外的历史计划/规范不包含旧 owner；guard 脚本自身保留精确 allowlist 以检查禁止引用。
- 生产源码秘密扫描：未发现 PEM 私钥或带具体值的密码、passphrase、token；resolver 的 token 仅作为内存配置字段，测试 fixture 不属于生产凭据。
- `git diff --check`：**PASS**，无空白错误。

## 真实主机验收门槛

真实验收 harness 必须在 `AURORA_AIOPS_E2E_HOST` 指向明确标记为 disposable 的 Ubuntu 22.04/24.04 或 Debian 12 主机时才允许运行；缺少该变量或 disposable 标记时必须安全跳过并返回“未运行”，不得返回伪成功。本轮变量缺失，故以下项目均未执行：

- SSH 主机密钥确认、root/非交互 sudo 和 `/etc/kubernetes/admin.conf` 检查；
- 真实 release 下载、上传、远端 SHA-256、解包、systemd enable/restart 与本机健康接口；
- 浏览器刷新后的 SSE 重放、取消边界、retry 幂等和旧版本 `current` 回滚；
- cleanup、任务 ID、release digest、OS/arch 和服务日志采样。

后续目标机验收必须单独记录上述证据，严禁记录 bootstrap 密码、GitHub token、环境文件内容或 kubeconfig。
