# Aurora AIOps Project Installation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish verifiable Linux release assets and install a selected Aurora AIOps version on a supported server through the durable task engine and project-center UI.

**Architecture:** An `aurora-aiops` installer registers fixed project metadata and builds nine idempotent steps. A release resolver reads only the configured `hougangbei/aurora-aiops` GitHub repository, selects an exact version and architecture, and verifies `checksums.txt`. The control plane downloads to a private temporary file, verifies it locally, uploads it through the verified SSH transport, and the target performs a staged atomic activation under `/opt/aurora-aiops`. Strictly normalized bootstrap-admin configuration is encrypted in the task record and never returned. No browser or request value can supply a URL, path, service unit or shell command.

**Tech Stack:** Go 1.25, GitHub Releases HTTP API, SHA-256, SSH/SFTP-style streaming, systemd, React 19, TypeScript, TanStack Query, Ant Design, Vitest, GitHub Actions.

---

## Fixed project and step contract

Project ID is `aurora-aiops`. Supported targets are Linux `amd64` and `arm64` with systemd and a healthy local Kubernetes admin configuration at `/etc/kubernetes/admin.conf`; installing Kubernetes first satisfies this prerequisite. The project repository is the existing configured value `AURORA_AIOPS_UPDATE_REPOSITORY`, whose default remains `hougangbei/aurora-aiops`; authenticated requests reuse `AURORA_AIOPS_UPDATE_GITHUB_TOKEN` and never log it.

| Step ID | Label | Percent | Success probe |
| --- | --- | ---: | --- |
| `verify-ssh` | 校验连接与主机指纹 | 10 | verified SSH command succeeds |
| `preflight` | 检查系统、Kubernetes、架构、磁盘和权限 | 20 | Linux, systemd, healthy local kubeconfig, supported arch, 2 GiB free, root/passwordless sudo |
| `resolve-release` | 解析发布资产 | 30 | exact tag has archive and checksums for target arch |
| `transfer-release` | 传输发布包 | 40 | remote staging archive has expected byte count |
| `verify-checksum` | 校验 SHA256 | 50 | remote SHA-256 equals signed-in-task expected digest |
| `activate-version` | 解包并原子切换版本 | 65 | `current` resolves to selected version directory |
| `configure-service` | 配置环境与 systemd | 78 | generated files have expected content hash and modes |
| `restart-service` | 启用并重启服务 | 90 | systemd reports active and enabled |
| `verify-health` | 验证健康并记录安装 | 100 | local health endpoint reports selected build version |

### Task 1: Make release assets installer-compatible

**Files:**
- Modify: `scripts/build-release.sh`
- Create: `scripts/verify-release-assets.sh`
- Create: `scripts/verify-release-assets.test.sh`
- Modify: `.github/workflows/release.yml`
- Modify: `deploy/aurora-aiops.service`

- [ ] **Step 1: Write a failing archive contract test**

The shell test creates synthetic `linux_amd64`, `linux_arm64` and `darwin_arm64` archives plus `checksums.txt`, then verifies:

- Linux archives contain executable `aurora-aiops`, `aurora-aiops.service` and the migration script beneath one versioned top-level directory;
- names match `aurora-aiops_<version>_linux_<arch>.tar.gz`;
- every release archive has exactly one checksum entry and every checksum entry references an existing archive;
- duplicate names, missing Linux architectures, mismatched digest and path traversal members fail.

- [ ] **Step 2: Verify RED**

Run: `bash scripts/verify-release-assets.test.sh`

Expected: FAIL because the verifier does not exist.

- [ ] **Step 3: Implement the verifier and tighten packaging**

Implement `verify-release-assets.sh <directory> <version>` using `tar -tzf` and `sha256sum`/`shasum -a 256`. Reject absolute paths and any member containing `..`. Update `build-release.sh` so package modes are deterministic, archives do not include the `latest` symlink, and Linux package contents satisfy the contract. Keep the existing linux/amd64, linux/arm64 and darwin/arm64 matrix.

Update `aurora-aiops.service` so the installer can copy it unchanged to `/etc/systemd/system/aurora-aiops.service`, with `WorkingDirectory=/opt/aurora-aiops/current`, `ExecStart=/opt/aurora-aiops/current/aurora-aiops`, and `EnvironmentFile=-/etc/aurora-aiops/aurora-aiops.env`.

- [ ] **Step 4: Gate GitHub release publishing**

After artifact flattening and checksum creation, run `scripts/verify-release-assets.sh release-assets "${GITHUB_REF_NAME#v}"` before `softprops/action-gh-release`. Ensure `checksums.txt` is uploaded once.

- [ ] **Step 5: Verify GREEN and commit**

```bash
bash scripts/verify-release-assets.test.sh
shellcheck scripts/build-release.sh scripts/verify-release-assets.sh scripts/verify-release-assets.test.sh
git diff --check
```

If `shellcheck` is unavailable locally, record that fact but require it in the GitHub workflow. Expected: contract tests pass.

```bash
git add scripts/build-release.sh scripts/verify-release-assets.sh scripts/verify-release-assets.test.sh .github/workflows/release.yml deploy/aurora-aiops.service
git commit -m "build: verify installable release assets"
```

### Task 2: Resolve and download exact GitHub release assets

**Files:**
- Create: `server/internal/deployment/github_release.go`
- Create: `server/internal/deployment/github_release_test.go`
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/config/config_test.go`

- [ ] **Step 1: Write failing resolver tests**

Use `httptest.Server` and assert:

- version `0.1.2` requests tag `v0.1.2`, not `latest`;
- `amd64` selects only `aurora-aiops_0.1.2_linux_amd64.tar.gz` and `arm64` selects its exact peer;
- `checksums.txt` must contain exactly one matching digest;
- missing, duplicate, oversized and redirect-to-untrusted-host assets fail;
- configured GitHub token is sent only to `api.github.com` or the configured test API origin and never to a release download host;
- 403 rate-limit errors become `RELEASE_RATE_LIMITED` with a safe message suggesting the configured token;
- archive download is bounded and its local digest must match before returning.

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/deployment -run GitHubRelease -count=1`

Expected: resolver symbols are missing.

- [ ] **Step 3: Implement a narrow resolver**

Expose:

```go
type ReleaseArtifact struct {
	Version, Architecture, ArchiveName, DownloadURL, SHA256 string
	Size int64
}

type ReleaseResolver interface {
	Resolve(context.Context, string, string) (ReleaseArtifact, error)
	Download(context.Context, ReleaseArtifact, io.Writer) error
}

func NewGitHubReleaseResolver(repository, token string, client *http.Client) (*GitHubReleaseResolver, error)
```

Validate repository with `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, version with strict semver, architecture against `amd64|arm64`, HTTPS download hosts against GitHub's returned asset URL plus its validated redirect chain, a 1 GiB maximum archive, and a 1 MiB maximum checksum file. Hash while streaming to a mode-0600 temporary file. Do not reuse exported internals from the system updater; share only configuration because installer selection has stricter exact-version semantics.

Config tests must prove the resolver uses the current update repository default `hougangbei/aurora-aiops` and supports the user's override without introducing any `heihuzicity-tech` default or fixture.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/config ./internal/deployment -run 'GitHubRelease|UpdateRepository' -count=1`

Expected: PASS.

```bash
git add server/internal/deployment/github_release.go server/internal/deployment/github_release_test.go server/internal/config/config.go server/internal/config/config_test.go
git commit -m "feat(deployment): resolve verified Aurora releases"
```

### Task 3: Implement the Aurora installer plan

**Files:**
- Create: `server/internal/deployment/catalog/aurora.go`
- Create: `server/internal/deployment/catalog/aurora_test.go`
- Create: `server/internal/deployment/catalog/aurora_commands.go`
- Create: `server/internal/deployment/catalog/aurora_commands_test.go`
- Modify: `server/internal/deployment/model.go`
- Modify: `server/internal/deployment/worker.go`
- Modify: `server/internal/deployment/worker_test.go`

- [ ] **Step 1: Write failing project/plan tests**

Assert project metadata, supported architectures and server counts. For a supported server, assert all nine stable IDs, labels, percentages, timeouts and strictly increasing order. Assert unsupported OS, architecture, missing systemd and unknown version return `ErrUnsupportedTarget` before task creation.

- [ ] **Step 2: Write command-recorder tests**

Run every probe and action through a fake execution target that records uploads and exact commands. Prove:

- paths are limited to `/tmp/aurora-aiops/<task-id>/`, `/opt/aurora-aiops/releases/<version>/`, `/opt/aurora-aiops/current`, `/etc/aurora-aiops/aurora-aiops.env` and `/etc/systemd/system/aurora-aiops.service`;
- task/server/version fields cannot inject shell syntax;
- privilege selection is exactly root or `sudo -n` and preflight rejects interactive sudo;
- checksum mismatch stops before extraction;
- extraction first lists and validates members, then writes a new version directory, then atomically swaps a temporary symlink;
- repeated execution probes skip already-correct steps;
- the installer creates the locked `aurora-aiops` system user/group and owned data/config directories idempotently;
- `/etc/kubernetes/admin.conf` is copied to `/opt/aurora-aiops/config/kubeconfig` with ownership `aurora-aiops:aurora-aiops` and mode 0600;
- bootstrap username/password, service environment values and HTTP authorization never appear in commands, events, errors or recorder output.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment/catalog -run Aurora -count=1`

Expected: package is missing.

- [ ] **Step 4: Add safe execution helpers**

Extend `ExecutionContext` with fixed operations rather than exposing credentials:

```go
type ExecutionContext interface {
	Server() assets.Server
	Run(context.Context, string, int64) (assets.CommandResult, error)
	Upload(context.Context, io.Reader, int64, string, fs.FileMode) error
	Log(string)
	SetValue(string, string) error
	Value(string) (string, bool)
}
```

`SetValue` stores only non-secret task-local values needed after restart in the Phase-2 `deployment_task_values` table: archive name, expected SHA-256 and staging path. Keys are allow-listed by the installer and values never enter API responses. Release bytes remain in a mode-0600 control-plane temp file only until upload completes and are removed on success, error or cancellation.

- [ ] **Step 5: Implement nine idempotent steps**

`NormalizeConfiguration` accepts exactly `{bootstrapAdminUser, bootstrapAdminPassword}`. Require a DNS-label-like username of 3–64 characters and a 12–128 character password; reject unknown/missing fields. Use constant command templates and strict POSIX quoting for validated scalar values. Generate the initial environment file locally, seed the redactor with both values, upload through stdin, chown to the service account and apply mode 0600. After the first healthy startup creates the account, rewrite the environment file without bootstrap credentials, restart once, and recheck health; encrypted task configuration remains available only for safe retry. Installation record is written only after `systemctl is-active`, `systemctl is-enabled`, and a bounded loopback health request report the selected version. On failure retain the previous `current` symlink and service whenever activation has not completed; document that restart-stage failures require retry.

- [ ] **Step 6: Register the installer and verify GREEN**

Construct `AuroraInstaller` in `server.Run` with current update repository/token config and register it in the deployment catalog. Run:

```bash
cd server
go test -race ./internal/deployment/... -run Aurora -count=1
go test ./internal/deployment/... -count=1
```

Expected: PASS.

```bash
git add server/internal/deployment server/internal/server/server.go
git commit -m "feat(deployment): install Aurora AIOps remotely"
```

### Task 4: Add the project center and install flow

**Files:**
- Create: `web/src/modules/projects/types.ts`
- Create: `web/src/modules/projects/api.ts`
- Create: `web/src/modules/projects/api.test.ts`
- Create: `web/src/modules/projects/components/InstallProjectModal.tsx`
- Create: `web/src/modules/projects/components/InstallProjectModal.test.tsx`
- Create: `web/src/pages/AssetProjectsPage.tsx`
- Create: `web/src/pages/AssetProjectsPage.test.tsx`
- Create: `web/src/pages/AssetProjectDetailsPage.tsx`
- Create: `web/src/pages/AssetProjectDetailsPage.test.tsx`
- Modify: `web/src/router/AppRouter.tsx`
- Modify: `web/src/layouts/navigation.tsx`
- Modify: `web/src/layouts/navigation.test.tsx`

- [ ] **Step 1: Write API and page tests**

Assert exact `/api/v1/projects` URLs, project cards, description, recommended version, architectures and installed-server count. Details tests assert supported versions/targets and installation history. Route tests assert `/assets/projects` and `/assets/projects/:projectId`.

- [ ] **Step 2: Write install-modal tests**

Prove the modal filters incompatible/offline/unconfirmed-host-key servers, explains every disabled reason, defaults to the recommended exact version, requires bootstrap admin username plus password/confirmation, clears password fields on close, requires a summary confirmation, submits `{serverId, version, configuration:{bootstrapAdminUser, bootstrapAdminPassword}}`, handles active-task conflict, opens `TaskProgressDrawer` for the returned task, and disables the action for viewer/operator/demo mode.

- [ ] **Step 3: Verify RED**

Run: `cd web && npm test -- src/modules/projects src/pages/AssetProjectsPage.test.tsx src/pages/AssetProjectDetailsPage.test.tsx`

Expected: modules/pages are missing.

- [ ] **Step 4: Implement project center and install flow**

Use TanStack Query keys `['projects']`, `['project', id]`, and `['asset-servers']`. Show Aurora branding, supported architectures and exact version. Invalidate project, server tasks and installations after task terminal state. Preserve the task ID in the URL so a refresh reconnects.

- [ ] **Step 5: Verify GREEN and commit**

```bash
cd web
npm test
npm run build
```

Expected: PASS.

```bash
git add web/src/modules/projects web/src/pages/AssetProjectsPage.tsx web/src/pages/AssetProjectsPage.test.tsx web/src/pages/AssetProjectDetailsPage.tsx web/src/pages/AssetProjectDetailsPage.test.tsx web/src/router/AppRouter.tsx web/src/layouts/navigation.tsx web/src/layouts/navigation.test.tsx
git commit -m "feat(web): add Aurora project installation"
```

### Task 5: Aurora installation acceptance and documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/aiops/api-v1.md`
- Create: `docs/aiops/operations/aurora-remote-install.md`
- Create: `docs/aiops/acceptance/2026-08-09-aurora-project-installation.md`

- [ ] **Step 1: Document the operator contract**

Document repository/token configuration, supported architectures, local Kubernetes prerequisite, bootstrap-account lifecycle, root/passwordless-sudo requirement, exact target paths, systemd behavior, health verification, retry behavior, old-version retention and manual rollback by changing `current`. State explicitly that the default and examples use `hougangbei/aurora-aiops`, never `heihuzicity-tech`.

- [ ] **Step 2: Run automated phase gate**

```bash
bash scripts/verify-release-assets.test.sh
cd server && gofmt -w internal/deployment internal/server internal/config
cd server && go test -race ./internal/deployment/...
cd server && go test ./...
cd server && go vet ./...
cd web && npm test
cd web && npm run build
./scripts/verify-brand-rename.sh
git grep -n 'heihuzicity-tech' -- . ':!docs/superpowers/specs/*' ':!docs/superpowers/plans/*'
git diff --check
git grep -In -E '(BEGIN (RSA|OPENSSH) PRIVATE KEY|password[=:][^*]|passphrase[=:][^*]|token[=:][^*])' -- server web docs ':!**/*_test.go'
```

Expected: all tests pass; both repository-name and secret scans return no matches.

- [ ] **Step 3: Run controlled Linux acceptance**

Against disposable Ubuntu 22.04/24.04 or Debian 12 hosts explicitly supplied through `AURORA_AIOPS_E2E_HOST`, install one published version, verify all nine events, systemd active/enabled, `/api/v1/system/version`, refresh/reconnect, and retry idempotency. The acceptance harness must refuse to run if the environment variable is absent or the host is not marked disposable.

- [ ] **Step 4: Record evidence and commit**

Record archive names/digests, target OS/arch, task ID, event sequence, service/health probes and cleanup decision. Do not record credentials, environment file contents or kubeconfig.

```bash
git add README.md docs/aiops
git commit -m "docs: record Aurora remote install acceptance"
```
