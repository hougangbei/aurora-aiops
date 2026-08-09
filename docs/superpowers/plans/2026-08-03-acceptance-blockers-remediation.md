# 验收阻塞问题修复实施计划

> 执行方式：在 `feat/authorization-consistency-closure` 隔离 worktree 中按 TDD 逐项修复；不直接修改 `main`，不推送远端。

**目标：** 关闭权限与一致性封口任务独立验收发现的 4 个 P1 和 3 个 P2 问题，并以自动化测试、清单静态验证和构建结果作为验收证据。

**原则：** 每项修复先加入能稳定复现问题的失败测试，再做最小生产代码修改。默认集群身份保持只读和最小权限；审批状态与审计记录必须在同一个 SQLite 事务中提交或回滚；所有错误脱敏必须保留 `errors.Is`/`errors.As` 链。

## 任务 1：Secret 读取授权与默认只读 RBAC

**文件：**

- 修改 `server/internal/server/platform_rbac_test.go`
- 修改 `server/internal/server/platform_rbac_integration_test.go`
- 修改 `server/internal/server/platform_rbac.go`
- 修改 `server/internal/server/router.go`
- 修改 `scripts/verify-kubernetes-manifests.sh`
- 修改 `deploy/kubernetes/rbac.yaml`

**步骤：**

1. 增加策略单测，断言 `GET /api/v1/secrets/:namespace/:name/yaml` 只允许 admin。
2. 增加路由集成测试，断言 viewer 请求 Secret YAML 返回 403，且不会进入资源处理器。
3. 在中央策略中将 Secret YAML GET 归入 admin，并在具体路由追加 `RequireAdmin()` 作为纵深防御。
4. 先扩展清单验证脚本并观察其失败：默认 ClusterRole 不得授予 `secrets`，必须包含应用实际读取的资源和子资源。
5. 从默认 ClusterRole 移除 `secrets`；加入 `pods/log`、RBAC 资源、`resourcequotas`、`limitranges`、核心组 `persistentvolumes` 和 VPA 只读权限；修正 `persistentvolumes` 的 API 组。
6. 运行：

   ```bash
   cd server && go test ./internal/server -run 'Secret|PlatformRBAC'
   KUBECONFIG=/Volumes/gang/work/k8s2/aurora-aiops/.dev-kubeconfig/kubeconfig ./scripts/verify-kubernetes-manifests.sh
   ```

## 任务 2：集群内身份为 kubectl 生成安全运行时 kubeconfig

**文件：**

- 修改 `server/internal/kube/client_test.go`
- 修改 `server/internal/kube/client.go`
- 修改 `deploy/kubernetes/deployment.yaml`
- 修改 `scripts/verify-kubernetes-manifests.sh`

**步骤：**

1. 修改身份优先级测试，要求 in-cluster 客户端返回非空 `ConfigPath`。
2. 增加真实写盘单测，断言生成文件权限为 `0600`、引用 ServiceAccount `tokenFile`、不嵌入 Bearer Token，并保留服务端地址和 CA 配置。
3. 为测试注入运行时 kubeconfig 写入器；生产实现使用 client-go 配置 API 生成仅供 kubectl 使用的短生命周期 kubeconfig。
4. 写入位置由 `AURORA_AIOPS_RUNTIME_DIR` 控制，默认退回 `os.TempDir()`；使用临时文件和 `0600` 权限。
5. Deployment 增加内存型 `emptyDir` 并挂载 `/var/run/aurora-aiops`，设置 `AURORA_AIOPS_RUNTIME_DIR=/var/run/aurora-aiops`，保持根文件系统只读。
6. 扩展清单验证，要求运行时目录、内存卷和挂载存在，同时继续禁止外部 kubeconfig Secret/ConfigMap 挂载。
7. 运行：

   ```bash
   cd server && go test ./internal/kube
   KUBECONFIG=/Volumes/gang/work/k8s2/aurora-aiops/.dev-kubeconfig/kubeconfig ./scripts/verify-kubernetes-manifests.sh
   ```

## 任务 3：审批状态与审计记录原子提交

**文件：**

- 修改 `server/internal/audit/repository.go`
- 新建 `server/internal/remediation/decision_store.go`
- 新建 `server/internal/remediation/decision_store_test.go`
- 修改 `server/internal/remediation/service.go`
- 修改 `server/internal/remediation/service_test.go`
- 修改 `server/internal/server/server.go`
- 修改 `server/internal/server/platform_rbac_integration_test.go`
- 修改 `server/internal/server/aiops_evidence_routes_test.go`

**步骤：**

1. 增加 SQLite 回滚测试：用触发器强制 `audit_records` 插入失败，审批调用必须返回错误，incident 仍为 `awaiting_approval`，审计表仍为空。
2. 将审计追加核心逻辑抽取为接收 `*sql.Tx` 的 `audit.AppendTx`；原 `Repository.Append` 仍自行开启和提交事务。
3. 新增 remediation `DecisionStore`：在一个事务中执行 `awaiting_approval` 条件更新、区分 not found/冲突、写入审计链、提交事务。
4. `Service.Approve`/`Reject` 改用 `DecisionStore`，保留批准前最新策略复核；并发决策仍由数据库 CAS 决定唯一赢家。
5. 所有服务装配点注入 `DecisionStore`。
6. 运行：

   ```bash
   cd server && go test ./internal/audit ./internal/remediation ./internal/server
   cd server && go test -race ./internal/remediation -run 'Concurrent|Atomic|Rollback' -count=20
   ```

## 任务 4：前后端审批输入 fail-closed

**文件：**

- 修改 `web/src/modules/aiops/components/ApprovalDialog.test.tsx`
- 修改 `web/src/modules/aiops/components/ApprovalDialog.tsx`
- 修改 `server/internal/server/aiops_routes_test.go`
- 修改 `server/internal/server/aiops_routes.go`

**步骤：**

1. 增加前端测试：非法风险枚举、非数组 blockers、畸形 action/modelReview 均不得显示批准按钮且不得抛异常。
2. 用显式 type guards 验证风险枚举、布尔值、字符串数组、动作字段和模型评审字段；`approvable=true` 时 actions 必须非空。
3. 增加后端测试：空白、7 个 Unicode 字符理由被拒绝，8 个字符理由通过并以 trim 后值传入服务；批准与拒绝共享相同规则。
4. 后端使用 rune 数量校验 trim 后理由，最少 8 个字符。
5. 运行：

   ```bash
   cd web && npm test -- --run src/modules/aiops/components/ApprovalDialog.test.tsx
   cd server && go test ./internal/server -run 'Approve|Reject|Reason'
   ```

## 任务 5：配置错误脱敏且保留错误链

**文件：**

- 修改 `server/internal/kube/client_test.go`
- 修改 `server/internal/kube/client.go`

**步骤：**

1. 增加 sentinel error 测试：显式 kubeconfig 路径被脱敏，同时 `errors.Is` 仍为 true。
2. 增加 in-cluster 失败测试：ServiceAccount token/CA 路径不得出现在最终错误中，同时保留原始 in-cluster sentinel error。
3. 实现带 `Unwrap()` 的路径脱敏错误类型，并在所有 explicit、fallback、in-cluster 错误组合前统一脱敏。
4. 运行：

   ```bash
   cd server && go test ./internal/kube -run 'Redact|Error|Precedence'
   ```

## 最终验收

1. 格式化所有修改的 Go 文件并检查工作区差异。
2. 执行：

   ```bash
   cd server && go vet ./...
   cd server && go test -race ./...
   cd web && npm ci
   cd web && npm test -- --run
   cd web && npm run build
   KUBECONFIG=/Volumes/gang/work/k8s2/aurora-aiops/.dev-kubeconfig/kubeconfig ./scripts/verify-kubernetes-manifests.sh
   ./scripts/build-release.sh
   git status --short
   ```

3. 复核 `git diff f88ed88...HEAD`，确认只包含上述修复、测试、清单和计划文档；不推送、不合并、不清理 worktree。
