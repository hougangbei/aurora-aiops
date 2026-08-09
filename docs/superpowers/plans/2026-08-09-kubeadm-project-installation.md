# Single-Node kubeadm Project Installation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a guarded one-click installation of a fixed single-node Kubernetes stack, plus explicit read-only adoption of an existing kubeadm cluster, encrypted kubeconfig storage, durable progress and health summaries.

**Architecture:** A built-in `kubernetes` installer emits ten fixed, idempotent steps and accepts only a server ID and catalog version. Commands are generated from compile-time constants for Ubuntu 22.04/24.04 and Debian 12, with root or passwordless sudo. Existing clusters stop installation with an adoption-required result; a separate admin action performs read-only health checks and encrypts kubeconfig. No reset/uninstall path exists.

**Tech Stack:** Go 1.25, SQLite, AES-256-GCM, SSH, containerd, kubeadm/kubelet/kubectl, Cilium, React 19, TypeScript, TanStack Query, Ant Design, Vitest.

---

## Fixed supported stack

- Kubernetes `v1.35.6`, apt repository minor `v1.35`, package version `1.35.6-1.1`.
- Cilium `v1.19.4`, installed with checksum-verified Cilium CLI `v0.19.2`.
- containerd from the target distribution, configured with `SystemdCgroup = true`.
- pod CIDR `10.244.0.0/16`; service CIDR remains kubeadm's default.
- Ubuntu `22.04`, Ubuntu `24.04`, Debian `12`; Linux kernel at least `5.10`; `amd64` and `arm64`.
- Minimum 2 CPU cores, 4 GiB RAM and 30 GiB free on `/`; required TCP ports 6443, 2379-2380, 10250, 10257 and 10259 must not already be occupied.

These versions are constants in the catalog. Updating them requires a new tested code change; the installer never resolves `latest`.

| Step ID | Label | Percent |
| --- | --- | ---: |
| `verify-access` | 校验 SSH、指纹和权限 | 5 |
| `preflight` | 检查系统、资源、端口和网络 | 15 |
| `prepare-kernel` | 配置内核、sysctl 和 swap | 28 |
| `install-containerd` | 安装并配置 containerd | 45 |
| `install-kube-tools` | 安装固定版本 Kubernetes 组件 | 60 |
| `kubeadm-init` | 初始化控制平面 | 75 |
| `configure-single-node` | 配置 kubeconfig 和单节点调度 | 80 |
| `install-cilium` | 安装固定版本 Cilium | 88 |
| `wait-ready` | 等待节点和核心 Pod 就绪 | 96 |
| `save-cluster` | 加密保存 kubeconfig 和健康摘要 | 100 |

### Task 1: Add encrypted deployment-secret storage

**Files:**
- Modify: `server/internal/store/migrate.go`
- Modify: `server/internal/store/migrate_test.go`
- Modify: `server/internal/deployment/secret.go`
- Modify: `server/internal/deployment/secret_test.go`
- Modify: `server/internal/deployment/repository.go`
- Modify: `server/internal/deployment/repository_test.go`

- [ ] **Step 1: Write failing schema and cipher tests**

Assert `deployment_secrets` exists, has no plaintext column, and enforces one `kubeconfig` secret per server/project. Cipher tests round-trip arbitrary bytes, bind associated data to kind/server/project, reject a 31-byte key, reject tampering and prove neither plaintext nor base64 plaintext appears in the envelope.

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/store ./internal/deployment -run 'DeploymentSecret|SecretCipher' -count=1`

Expected: schema and cipher are absent.

- [ ] **Step 3: Add schema and implementation**

Add:

```sql
CREATE TABLE IF NOT EXISTS deployment_secrets (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  secret_kind TEXT NOT NULL CHECK (secret_kind IN ('kubeconfig')),
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(server_id, project_id, secret_kind)
);
```

Extend the Phase-2 cipher with `SealResource(kind, serverID, projectID string, plaintext []byte) (SealedSecret, error)` and `OpenResource(kind, serverID, projectID string, sealed SealedSecret) ([]byte, error)`. Associated data is `aurora-aiops/deployment-secret/v1\x00<kind>\x00<serverID>\x00<projectID>`. Reuse `AURORA_AIOPS_ASSET_ENCRYPTION_KEY`; never introduce a fallback or generated ephemeral production key. Repository methods are `PutSecret`, `HasSecret`, and internal-only `OpenSecret`; no HTTP response exposes envelope fields.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/store ./internal/deployment -run 'DeploymentSecret|SecretCipher' -count=1`

Expected: PASS.

```bash
git add server/internal/store server/internal/deployment/secret.go server/internal/deployment/secret_test.go server/internal/deployment/repository.go server/internal/deployment/repository_test.go
git commit -m "feat(deployment): encrypt managed cluster kubeconfigs"
```

### Task 2: Encode and validate the fixed kubeadm plan

**Files:**
- Create: `server/internal/deployment/catalog/kubernetes.go`
- Create: `server/internal/deployment/catalog/kubernetes_test.go`
- Create: `server/internal/deployment/catalog/kubeadm_preflight.go`
- Create: `server/internal/deployment/catalog/kubeadm_preflight_test.go`
- Create: `server/internal/deployment/catalog/kubeadm_commands.go`
- Create: `server/internal/deployment/catalog/kubeadm_commands_test.go`

- [ ] **Step 1: Write failing metadata and preflight tests**

Assert the exact versions, OS/architecture matrix, ten step IDs/labels/percentages/timeouts and final 100. Table-test supported `/etc/os-release` fixtures and reject Ubuntu 20.04/26.04, Debian 11/13, non-Linux, kernel below 5.10, fewer than 2 CPUs, less than 4 GiB RAM, less than 30 GiB disk, no default route/DNS, occupied required ports, interactive sudo and swap that cannot be disabled persistently.

Existing `/etc/kubernetes/admin.conf` or successful `kubectl get --raw=/readyz` must return typed `ErrAdoptionRequired` with collected version/node summary before any mutating command.

- [ ] **Step 2: Write failing command-generation tests**

For Ubuntu and Debian fixtures, record commands and prove:

- request/task/server strings never become shell fragments;
- every mutation uses root or the exact prefix `sudo -n --`;
- modules are `overlay` and `br_netfilter`; sysctls are fixed and written atomically;
- swap is disabled now and matching `/etc/fstab` swap entries are commented idempotently with a backup;
- containerd config is generated from `containerd config default`, then `SystemdCgroup = true` is verified;
- apt keyrings and sources use fixed HTTPS origins and signed-by files, never `curl | sh` or `apt-key`;
- kubelet/kubeadm/kubectl are installed at `1.35.6-1.1` and held;
- kubeadm config uses `v1beta4`, Kubernetes `v1.35.6`, pod CIDR `10.244.0.0/16`, containerd socket and a validated hostname;
- no command contains `kubeadm reset`, unbounded package upgrades or `latest`.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment/catalog -run 'Kubernetes|Kubeadm|Preflight' -count=1`

Expected: implementation is absent.

- [ ] **Step 4: Implement pure parsing and command builders**

Keep preflight parsing pure and command builders private. Use fixed files uploaded via `ExecutionContext.Upload` instead of shell interpolation for sysctl, modules, repository and kubeadm configuration. Validate hostname with the DNS-1123 subdomain rule before embedding it. Timeouts are 30 seconds for probes, 5 minutes for repository/package preparation, 10 minutes for kubeadm init/Cilium, and 15 minutes for readiness.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment/catalog -run 'Kubernetes|Kubeadm|Preflight' -count=1`

Expected: PASS.

```bash
git add server/internal/deployment/catalog/kubernetes.go server/internal/deployment/catalog/kubernetes_test.go server/internal/deployment/catalog/kubeadm_preflight.go server/internal/deployment/catalog/kubeadm_preflight_test.go server/internal/deployment/catalog/kubeadm_commands.go server/internal/deployment/catalog/kubeadm_commands_test.go
git commit -m "feat(deployment): define guarded kubeadm plan"
```

### Task 3: Implement idempotent Kubernetes execution

**Files:**
- Create: `server/internal/deployment/catalog/kubeadm_installer.go`
- Create: `server/internal/deployment/catalog/kubeadm_installer_test.go`
- Create: `server/internal/deployment/catalog/cilium.go`
- Create: `server/internal/deployment/catalog/cilium_test.go`
- Modify: `server/internal/deployment/repository.go`
- Modify: `server/internal/deployment/repository_test.go`
- Modify: `server/internal/server/server.go`

- [ ] **Step 1: Write fake-remote end-to-end tests**

Execute all ten steps against a stateful command recorder. After each simulated crash boundary, rebuild the plan and resume; assert completed work is probed/skipped and progress never decreases. Prove failure at package install, kubeadm init, Cilium and readiness produces safe typed errors and preserves logs after redaction.

Probe success criteria are exact: expected sysctl values; active containerd with CRI endpoint; exact held package versions; valid admin kubeconfig plus `/readyz`; control-plane taint absent; Cilium DaemonSet image tag `v1.19.4`; Node Ready; all non-completed `kube-system` Pods Ready. Exit code alone is insufficient.

- [ ] **Step 2: Write supply-chain tests for Cilium CLI**

Use an HTTP fixture to download `cilium-linux-amd64.tar.gz` or `cilium-linux-arm64.tar.gz` for CLI `v0.19.2` and its `.sha256sum`. Reject redirect to an untrusted origin, unexpected file names, size above 256 MiB and digest mismatch. The install invocation must be exactly `cilium install --version 1.19.4 --set ipam.mode=kubernetes` against `/etc/kubernetes/admin.conf`; retries use `cilium status --wait` before deciding whether installation is required.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment/catalog -run 'KubeadmInstaller|Cilium' -count=1`

Expected: executor symbols are absent.

- [ ] **Step 4: Implement all ten steps and installation recording**

Use the Phase-2 worker cancellation context for every remote command. Persist only allow-listed non-secret plan values. The final step reads `/etc/kubernetes/admin.conf` with a 1 MiB limit, validates it as YAML with exactly one current context and HTTPS server, seals it, and atomically upserts the encrypted secret plus `project_installations` status/version/health/task ID. Zero the local byte slice after sealing.

Register the installer in `server.Run`. The catalog returns compatibility reasons so the UI disables unsupported/offline/unconfirmed targets before submission, while the service repeats the same checks authoritatively.

- [ ] **Step 5: Verify GREEN and commit**

```bash
cd server
go test -race ./internal/deployment/... -run 'Kubernetes|Kubeadm|Cilium' -count=1
go test ./internal/deployment/... -count=1
```

Expected: PASS.

```bash
git add server/internal/deployment server/internal/server/server.go
git commit -m "feat(deployment): install single-node Kubernetes"
```

### Task 4: Add explicit existing-cluster adoption

**Files:**
- Create: `server/internal/deployment/adoption.go`
- Create: `server/internal/deployment/adoption_test.go`
- Modify: `server/internal/server/deployment_routes.go`
- Modify: `server/internal/server/deployment_routes_test.go`
- Modify: `server/internal/server/platform_rbac_test.go`
- Modify: `server/internal/server/platform_rbac_integration_test.go`

- [ ] **Step 1: Write failing adoption service tests**

Assert adoption rejects absent/unhealthy kubeconfig, unsupported Kubernetes version, hostname mismatch and an active deployment task. For a healthy cluster, assert only read commands run, kubeconfig is encrypted/upserted, installation status becomes `managed`, health summary records version/node/core-Pod counts, and audit includes metadata but no kubeconfig/server/token/certificate values.

- [ ] **Step 2: Write route/RBAC tests**

Add `POST /api/v1/projects/kubernetes/adopt` with body exactly `{serverId}`. Viewer/operator receive 403; admin receives a summary that contains `serverId`, project/version/status/health only. Assert invalid existing cluster is 422 `KUBERNETES_ADOPTION_FAILED` and active task is 409.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment ./internal/server -run 'Adopt|RequiredPlatformRoles|ClassifiesEveryRegisteredRoute' -count=1`

Expected: route/service are missing.

- [ ] **Step 4: Implement a read-only adoption boundary**

Adoption creates a normal deployment task with action `adopt`, so the existing partial unique index provides the server lock and the UI gets durable progress. The Kubernetes installer builds one read-only `inspect-existing-cluster` step for that action. It may run only fixed `test -s`, `kubectl version -o json`, `/readyz`, node and namespace/POD status commands; it must never call kubeadm, change taints, install Cilium, restart services or mutate host files.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment ./internal/server -run 'Adopt|RequiredPlatformRoles|ClassifiesEveryRegisteredRoute' -count=1`

Expected: PASS.

```bash
git add server/internal/deployment/adoption.go server/internal/deployment/adoption_test.go server/internal/server/deployment_routes.go server/internal/server/deployment_routes_test.go server/internal/server/platform_rbac_test.go server/internal/server/platform_rbac_integration_test.go
git commit -m "feat(deployment): adopt existing kubeadm clusters"
```

### Task 5: Complete Kubernetes project UI and warnings

**Files:**
- Create: `web/src/modules/projects/components/AdoptKubernetesModal.tsx`
- Create: `web/src/modules/projects/components/AdoptKubernetesModal.test.tsx`
- Modify: `web/src/modules/projects/api.ts`
- Modify: `web/src/modules/projects/api.test.ts`
- Modify: `web/src/modules/projects/components/InstallProjectModal.tsx`
- Modify: `web/src/modules/projects/components/InstallProjectModal.test.tsx`
- Modify: `web/src/pages/AssetProjectsPage.tsx`
- Modify: `web/src/pages/AssetProjectsPage.test.tsx`
- Modify: `web/src/pages/AssetProjectDetailsPage.tsx`
- Modify: `web/src/pages/AssetProjectDetailsPage.test.tsx`
- Modify: `web/src/pages/AssetServerDetailsPage.tsx`
- Modify: `web/src/pages/AssetServerDetailsPage.test.tsx`

- [ ] **Step 1: Write Kubernetes UI tests**

Assert the card shows Kubernetes/Cilium fixed versions, single-node scope, OS/resources and destructive-change warning. Install modal must surface each compatibility reason, require typing the server name before confirm, and state that cancellation does not revert completed host changes. An adoption-required API result opens the adoption modal instead of retrying installation.

Adoption tests show the discovered version/health after the adoption task finishes, require a second confirmation, submit only `{serverId}`, disable non-admin action and render the managed installation without exposing kubeconfig. Server detail shows version, node/core-Pod health, last task and managed status.

- [ ] **Step 2: Verify RED**

Run: `cd web && npm test -- src/modules/projects src/pages/AssetProjectsPage.test.tsx src/pages/AssetProjectDetailsPage.test.tsx src/pages/AssetServerDetailsPage.test.tsx`

Expected: Kubernetes/adoption assertions fail.

- [ ] **Step 3: Implement fixed-stack and adoption UX**

Keep all mutation buttons gated by the existing effective-role/demo-mode helpers. Use project compatibility data returned by the server, but render local fallback text for network errors. After adoption invalidate project, server and installation queries; never request or render the stored kubeconfig.

- [ ] **Step 4: Verify GREEN and commit**

```bash
cd web
npm test
npm run build
```

Expected: PASS.

```bash
git add web/src/modules/projects web/src/pages/AssetProjectsPage.tsx web/src/pages/AssetProjectsPage.test.tsx web/src/pages/AssetProjectDetailsPage.tsx web/src/pages/AssetProjectDetailsPage.test.tsx web/src/pages/AssetServerDetailsPage.tsx web/src/pages/AssetServerDetailsPage.test.tsx
git commit -m "feat(web): add Kubernetes install and adoption flow"
```

### Task 6: Disposable-VM acceptance, security gate and documentation

**Files:**
- Create: `scripts/acceptance/kubeadm-single-node.sh`
- Create: `scripts/acceptance/kubeadm-single-node.test.sh`
- Modify: `README.md`
- Modify: `docs/aiops/api-v1.md`
- Create: `docs/aiops/operations/kubeadm-single-node.md`
- Create: `docs/aiops/acceptance/2026-08-09-kubeadm-project-installation.md`

- [ ] **Step 1: Write safety tests for the acceptance harness**

The harness must refuse to run unless all are true: `AURORA_AIOPS_E2E_HOST` is non-empty and not localhost/current machine, `AURORA_AIOPS_E2E_DISPOSABLE=yes`, an explicit expected SSH host fingerprint is supplied, and the server is Ubuntu 22.04/24.04 or Debian 12. Test every refusal path without opening a network connection.

- [ ] **Step 2: Implement controlled acceptance checks**

The harness calls product APIs rather than duplicating installation commands. It records task/event IDs and polls until terminal; then it runs read-only SSH verification for exact component versions, services, Node Ready, core Pods Ready, Cilium status and encrypted-secret presence through a metadata-only API. It must not call `kubeadm reset` or delete cluster data during cleanup.

- [ ] **Step 3: Run automated full gate**

```bash
bash scripts/acceptance/kubeadm-single-node.test.sh
cd server && gofmt -w internal/deployment internal/server internal/store
cd server && go test -race ./internal/deployment/...
cd server && go test ./...
cd server && go vet ./...
cd web && npm test
cd web && npm run build
./scripts/verify-brand-rename.sh
git diff --check
git grep -In -E '(BEGIN (RSA|OPENSSH) PRIVATE KEY|apiVersion: v1.*clusters:|password[=:][^*]|passphrase[=:][^*]|token[=:][^*])' -- server web docs ':!**/*_test.go'
```

Expected: all commands exit 0 and the secret scan returns no matches.

- [ ] **Step 4: Run disposable VM matrix only with explicit authorization**

Run one fresh-host installation for each supported OS and at least one of each architecture across the matrix. Verify ten progress milestones, browser/server restart replay, cancel-and-retry behavior before kubeadm init, idempotent retry after kubeadm init, and explicit adoption on a separately prepared cluster. If disposable hosts are not supplied, record this gate as not run and do not claim end-to-end acceptance.

- [ ] **Step 5: Document and commit evidence**

Document fixed versions, network/port/resource requirements, remote file mutations, partial-cancellation semantics, adoption boundary, encryption key backup requirement, upgrade limitations and recovery steps. Record only sanitized host labels and hashes.

```bash
git add scripts/acceptance README.md docs/aiops
git commit -m "docs: record kubeadm deployment acceptance"
```
