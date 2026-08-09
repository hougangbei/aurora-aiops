# 第三方依赖与代码归属结论

> 本文件满足 `dsglm开发文档/01-Go基础与兼容迁移实施计划.md` 前置检查：
> 「将代码授权结论写入 `docs/third-party-and-ownership.md`」。
> 同时满足 `dsglm开发文档/README.md` 第六节「授权与原创性检查点」。

## 1. 授权结论

仓库 `hougangbei/aurora-aiops`（远程 `https://github.com/hougangbei/aurora-aiops.git`）
当前未包含 `LICENSE` 或 `NOTICE` 文件。

经负责人确认：**本仓库归团队所有，团队已获得在本项目「基于多智能体协同的云原生
智能运维系统」中进行正式开发，并将产物用于竞赛、软件著作权与论文的明确授权。**

据此可以在本基础上：

- 修改、扩展后端 Go 代码与前端 React 代码；
- 将 `qd` 的 AIOps 行为（告警、Incident、工具调用记录、ChatOps、模型配置、审批、
  审计）迁移并重新实现到 Go 主工程 `aurora-aiops`；
- 将最终单二进制产物用于交付、竞赛与论文展示。

`qd` 目录仅作为只读迁移参考，不继续扩展 `qd/backend`，也不让生产前端同时依赖
Node 与 Go。

## 2. 原创贡献

本阶段（AIOps Go 基础与兼容迁移）的全部新增代码为团队原创实现，包括：

- `server/internal/store`：SQLite 持久化、迁移与 Repository 基础；
- `server/internal/aiops`：Incident 领域模型、严格状态机与 Repository；
- `/api/v1/aiops/incidents` HTTP 路由与生命周期装配。

从 `qd` 迁移时遵循「先写契约测试，再在 Go 中重新实现」的原则：

- 不逐行翻译 JavaScript；
- 不复制 `qd/backend/src/live.js` 中的实验环境硬编码 SSH 通道（`NODE_PORTS`、
  `root@127.0.0.1`、本地端口 `7788—7790`），这些不属于目标后端的节点识别机制。

## 3. 第三方依赖（基线快照）

### 3.1 后端 Go（`server/go.mod` 直接依赖）

- `github.com/gin-gonic/gin` v1.12.0
- `k8s.io/api` v0.35.3
- `k8s.io/apimachinery` v0.35.3
- `k8s.io/client-go` v0.35.3
- `k8s.io/metrics` v0.35.3
- 本阶段计划引入：`modernc.org/sqlite`（纯 Go、CGO-free 的 SQLite 驱动，见 Task 2）

### 3.2 前端（`web/package.json` 主要依赖）

- React 19、TypeScript、Vite、Ant Design、Ant Design Pro Components、
  TanStack Query、Zustand、Axios、Tailwind CSS

完整传递依赖以各自的 `go.mod`、`go.sum`、`package.json`、`package-lock.json` 为准。
