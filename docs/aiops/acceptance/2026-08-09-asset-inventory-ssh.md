# 资产盘点 SSH Phase-1 验收记录

**验收日期：** 2026-08-11
**范围：** Phase-1 服务器资产盘点的配置、加密凭据、免 Agent SSH 连接/主机密钥确认、采集、HTTP API、RBAC 与前端资产页面。本文只记录已完成命令的证据；不将其推断为生产环境主机验收。

## 已验证范围与限制

- 支持范围是 Linux 目标机、免 Agent SSH；认证方式为 `password` 和 `private_key`（私钥可配 `passphrase`）。未指定 SSH 端口时使用 `22`。
- 新服务器可在未连通目标机时登记为 `pending`；未知或变化的主机密钥必须经管理员确认。Phase-1 RBAC：viewer 读取，operator 测试连接和采集，admin 管理服务器与主机密钥确认。
- SSH 集成测试只针对进程内的 `127.0.0.1` loopback SSH server；本轮没有对真实主机执行验收，因此不声称已经完成 production host acceptance。
- 已知限制：SSH trusted roots 仍是固定边界；凭据主密钥由环境配置提供，尚未接入外部 key manager；没有 scheduler；installations 与 deployment task API 尚未实现。

## 命令证据

以下命令在本验收轮执行。Go 测试按包输出记录，不虚构跨包测试总数。

| 命令 | 结果 |
| --- | --- |
| `cd server && go test ./... -count=1` | exit 0；见下方逐包输出。 |
| `cd server && go vet ./...` | exit 0；无输出。 |
| `cd web && npm test` | exit 0；`26` files passed、`85` tests passed。运行时有既有 jsdom `getComputedStyle()` pseudo-element 未实现的 stderr 提示，未造成失败。 |
| `cd web && npm run build` | exit 0；TypeScript 与 Vite 构建成功。保留既有 Vite minified chunk `>500 kB` warning（主 JS `4,786.79 kB`，gzip `1,404.91 kB`）。 |
| `./scripts/verify-brand-rename.sh` | exit 0；输出 `Aurora AIOps brand guard passed`、`Repository ownership guard passed`。脚本为开发文档中的精确 deprecated 资产配置兼容变量和三条已有的“不得使用旧 owner”规划说明设窄 allowlist；交付代码和普通文档仍受 owner guard 约束。负向检查临时将旧 owner 写入 README，脚本按预期 exit 1；还原后再次通过。 |
| `git diff --check` | exit 0；无输出。 |
| `! git grep -InE '(BEGIN (RSA|OPENSSH|EC) PRIVATE KEY|AKIA[[:alnum:]]{16}|ghp_[[:alnum:]]{36}|sk-[[:alnum:]]{20,}|xox[baprs]-[[:alnum:]-]{20,})' -- server web ':(exclude)**/*_test.go' ':(exclude)**/*.test.ts' ':(exclude)**/*.test.tsx'; ! git grep -InP "(?:password|passphrase|token)\s*[:=]\s*['\"][^<*]" -- server web ':(exclude)**/*_test.go' ':(exclude)**/*.test.ts' ':(exclude)**/*.test.tsx'` | exit 0；生产源码敏感扫描无命中。 |

`go test ./... -count=1` 逐包输出（全部 PASS 或 `[no test files]`）：

```text
?   github.com/hougangbei/aurora-aiops/server/cmd/aurora-aiops [no test files]
ok  github.com/hougangbei/aurora-aiops/server/internal/aiops 1.447s
ok  github.com/hougangbei/aurora-aiops/server/internal/assets 3.238s
ok  github.com/hougangbei/aurora-aiops/server/internal/audit 1.372s
ok  github.com/hougangbei/aurora-aiops/server/internal/auth 2.501s
?   github.com/hougangbei/aurora-aiops/server/internal/buildinfo [no test files]
ok  github.com/hougangbei/aurora-aiops/server/internal/cluster 2.732s
ok  github.com/hougangbei/aurora-aiops/server/internal/config 3.154s
ok  github.com/hougangbei/aurora-aiops/server/internal/evidence 3.842s
ok  github.com/hougangbei/aurora-aiops/server/internal/experiment 4.141s
ok  github.com/hougangbei/aurora-aiops/server/internal/jsonx 4.512s
ok  github.com/hougangbei/aurora-aiops/server/internal/kube 5.088s
ok  github.com/hougangbei/aurora-aiops/server/internal/llm 7.270s
ok  github.com/hougangbei/aurora-aiops/server/internal/policy 4.837s
?   github.com/hougangbei/aurora-aiops/server/internal/ptyx [no test files]
ok  github.com/hougangbei/aurora-aiops/server/internal/remediation 4.555s
ok  github.com/hougangbei/aurora-aiops/server/internal/response 4.370s
ok  github.com/hougangbei/aurora-aiops/server/internal/server 14.138s
ok  github.com/hougangbei/aurora-aiops/server/internal/service 7.360s
ok  github.com/hougangbei/aurora-aiops/server/internal/store 5.134s
?   github.com/hougangbei/aurora-aiops/server/internal/web [no test files]
```

## 并发与竞态证据

引用 Task 6 已完成的命令证据：`go test -race ./internal/assets` PASS，`assets` 包耗时 `8.555s`；`go test -race ./internal/server` PASS，`server` 包耗时 `90.989s`。本验收文件只引用该已完成证据，不将其表述为本轮重新执行。
