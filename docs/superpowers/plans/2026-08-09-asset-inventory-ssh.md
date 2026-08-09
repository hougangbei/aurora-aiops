# Asset Inventory and Agentless SSH Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add offline-first server assets, encrypted SSH credentials, host-key confirmation, status/software collection, protected APIs, and asset list/detail UI.

**Architecture:** A new `server/internal/assets` package owns the domain and depends only on SQLite, a credential cipher, an SSH transport interface, and the audit repository. HTTP handlers translate JSON to domain inputs and never receive decrypted credentials. The frontend uses a dedicated `modules/assets` API/types layer and existing list/detail visual patterns.

**Tech Stack:** Go 1.25, SQLite, AES-256-GCM, `golang.org/x/crypto/ssh`, Gin, React 19, TanStack Query, Ant Design, Vitest.

---

## File map

- `server/internal/assets/model.go`: asset, credential metadata, snapshot and software public/domain types.
- `server/internal/assets/errors.go`: stable sentinel errors used by service and routes.
- `server/internal/assets/repository.go`: SQLite transactions and latest snapshot queries.
- `server/internal/assets/cipher.go`: AES-GCM credential envelope.
- `server/internal/assets/ssh.go`: host-key probe, verified SSH sessions, bounded command execution.
- `server/internal/assets/collector.go`: fixed read-only commands and normalized inventory parsing.
- `server/internal/assets/service.go`: validation, authorization-independent orchestration and audit calls.
- `server/internal/server/asset_routes.go`: JSON contracts and status/error mapping.
- `web/src/modules/assets/types.ts`: shared client types.
- `web/src/modules/assets/api.ts`: HTTP functions.
- `web/src/modules/assets/components/AddServerDrawer.tsx`: offline-first create flow.
- `web/src/pages/AssetServersPage.tsx`: inventory list.
- `web/src/pages/AssetServerDetailsPage.tsx`: tabs for overview/software/installations/tasks.

### Task 1: Add asset configuration and database schema

**Files:**
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/config/config_test.go`
- Modify: `server/internal/config/legacy_compat.go`
- Modify: `server/internal/store/migrate.go`
- Modify: `server/internal/store/migrate_test.go`

- [ ] **Step 1: Write failing config tests**

Add to `server/internal/config/config_test.go`:

```go
func TestLoadAssetConfig(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
	t.Setenv("AURORA_AIOPS_ASSET_ENCRYPTION_KEY", key)
	t.Setenv("AURORA_AIOPS_ASSET_COLLECT_INTERVAL", "10m")
	cfg, err := Load()
	if err != nil { t.Fatal(err) }
	if len(cfg.Asset.EncryptionKey) != 32 { t.Fatalf("key bytes=%d", len(cfg.Asset.EncryptionKey)) }
	if cfg.Asset.CollectInterval != 10*time.Minute { t.Fatalf("interval=%s", cfg.Asset.CollectInterval) }
}

func TestLoadRejectsInvalidAssetEncryptionKey(t *testing.T) {
	t.Setenv("AURORA_AIOPS_ASSET_ENCRYPTION_KEY", "not-base64")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ASSET_ENCRYPTION_KEY") {
		t.Fatalf("err=%v", err)
	}
}
```

Import `bytes` and `encoding/base64` alongside the existing test imports.

- [ ] **Step 2: Run the config tests and verify RED**

Run: `cd server && go test ./internal/config -run 'TestLoadAssetConfig|TestLoadRejectsInvalidAssetEncryptionKey' -count=1`

Expected: compile failure because `Config.Asset` and `AssetConfig` do not exist.

- [ ] **Step 3: Implement exact asset configuration parsing**

Add this public type and field in `server/internal/config/config.go`:

```go
type Config struct {
	// existing fields
	Asset AssetConfig
}

type AssetConfig struct {
	EncryptionKey  []byte
	CollectInterval time.Duration
}
```

Add:

```go
func loadAssetConfig() (AssetConfig, error) {
	raw := strings.TrimSpace(getEnvCompat("ASSET_ENCRYPTION_KEY", ""))
	var key []byte
	if raw != "" {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(decoded) != 32 {
			return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_ENCRYPTION_KEY must be base64-encoded 32 bytes")
		}
		key = decoded
	}
	interval, err := time.ParseDuration(getEnvCompat("ASSET_COLLECT_INTERVAL", "15m"))
	if err != nil || interval <= 0 {
		return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_COLLECT_INTERVAL must be a positive duration")
	}
	return AssetConfig{EncryptionKey: key, CollectInterval: interval}, nil
}
```

Call `loadAssetConfig()` near the start of `Load()` and assign it to `Config.Asset`. Add `encoding/base64` to imports.

- [ ] **Step 4: Write failing schema tests**

Add to `server/internal/store/migrate_test.go`:

```go
func TestOpenCreatesAssetSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "assets.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	want := []string{
		"asset_credentials", "asset_servers", "asset_snapshots",
		"asset_software_items", "project_installations",
	}
	for _, table := range want {
		var got string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&got); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}
```

- [ ] **Step 5: Run the schema test and verify RED**

Run: `cd server && go test ./internal/store -run TestOpenCreatesAssetSchema -count=1`

Expected: FAIL with `no rows in result set` for `asset_credentials`.

- [ ] **Step 6: Add the exact tables to `migrate.go`**

Append one `db.Exec` migration block before `return nil` containing:

```sql
CREATE TABLE IF NOT EXISTS asset_credentials (
  id TEXT PRIMARY KEY,
  auth_type TEXT NOT NULL CHECK (auth_type IN ('password','private_key')),
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS asset_servers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  address TEXT NOT NULL,
  ssh_port INTEGER NOT NULL CHECK (ssh_port BETWEEN 1 AND 65535),
  username TEXT NOT NULL,
  credential_id TEXT NOT NULL REFERENCES asset_credentials(id),
  host_key_fingerprint TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending','online','offline','error')),
  status_message TEXT NOT NULL DEFAULT '',
  os_family TEXT NOT NULL DEFAULT '',
  os_version TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  cpu_cores INTEGER NOT NULL DEFAULT 0,
  memory_bytes INTEGER NOT NULL DEFAULT 0,
  disk_bytes INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT NOT NULL DEFAULT '',
  last_collected_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS asset_snapshots (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  payload TEXT NOT NULL,
  collected_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_asset_snapshots_server_time
ON asset_snapshots(server_id, collected_at DESC);
CREATE TABLE IF NOT EXISTS asset_software_items (
  snapshot_id TEXT NOT NULL REFERENCES asset_snapshots(id) ON DELETE CASCADE,
  category TEXT NOT NULL,
  name TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (snapshot_id, category, name, architecture)
);
CREATE TABLE IF NOT EXISTS project_installations (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  install_path TEXT NOT NULL DEFAULT '',
  health_summary TEXT NOT NULL DEFAULT '',
  last_task_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(server_id, project_id)
);
```

Execute the SQL as one transaction-capable SQLite statement, matching the existing migration style.

- [ ] **Step 7: Verify GREEN and commit**

Run: `cd server && go test ./internal/config ./internal/store -count=1`

Expected: PASS.

Commit:

```bash
git add server/internal/config server/internal/store
git commit -m "feat(assets): add configuration and storage schema"
```

### Task 2: Implement encrypted credentials and domain models

**Files:**
- Create: `server/internal/assets/model.go`
- Create: `server/internal/assets/errors.go`
- Create: `server/internal/assets/cipher.go`
- Create: `server/internal/assets/cipher_test.go`

- [ ] **Step 1: Write the failing cipher tests**

Create `server/internal/assets/cipher_test.go`:

```go
package assets

import (
	"bytes"
	"strings"
	"testing"
)

func TestAESGCMCredentialCipherRoundTrip(t *testing.T) {
	c, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x31}, 32))
	if err != nil { t.Fatal(err) }
	want := CredentialSecret{Password: "p@ss", PrivateKey: "key", Passphrase: "phrase"}
	envelope, err := c.Encrypt(want)
	if err != nil { t.Fatal(err) }
	got, err := c.Decrypt(envelope)
	if err != nil { t.Fatal(err) }
	if got != want { t.Fatalf("got=%+v want=%+v", got, want) }
	if strings.Contains(string(envelope.Ciphertext), "p@ss") { t.Fatal("plaintext leaked") }
}

func TestAESGCMCredentialCipherRejectsTampering(t *testing.T) {
	c, _ := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x31}, 32))
	envelope, _ := c.Encrypt(CredentialSecret{Password: "secret"})
	envelope.Ciphertext[0] ^= 0xff
	if _, err := c.Decrypt(envelope); err == nil { t.Fatal("expected authentication failure") }
}

func TestAESGCMCredentialCipherRequires32ByteKey(t *testing.T) {
	if _, err := NewAESGCMCredentialCipher([]byte("short")); err == nil { t.Fatal("expected key error") }
}
```

- [ ] **Step 2: Run and verify RED**

Run: `cd server && go test ./internal/assets -run AESGCM -count=1`

Expected: compile failure because the `assets` package API is missing.

- [ ] **Step 3: Define stable domain types**

Create `model.go` with these exact exported contracts:

```go
package assets

import "time"

type ServerStatus string
const (
	ServerPending ServerStatus = "pending"
	ServerOnline ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerError ServerStatus = "error"
)

type CredentialAuthType string
const (
	AuthPassword CredentialAuthType = "password"
	AuthPrivateKey CredentialAuthType = "private_key"
)

type CredentialSecret struct { Password, PrivateKey, Passphrase string }
type CredentialEnvelope struct { Nonce, Ciphertext []byte; KeyVersion int }

type Server struct {
	ID, Name, Address, Username, CredentialID, HostKeyFingerprint string
	SSHPort int
	Status ServerStatus
	StatusMessage, OSFamily, OSVersion, Architecture string
	CPUCores int
	MemoryBytes, DiskBytes int64
	LastSeenAt, LastCollectedAt *time.Time
	CreatedAt, UpdatedAt time.Time
	CredentialAuthType CredentialAuthType
	CredentialConfigured bool
}

type CreateServerInput struct {
	Name, Address, Username string
	SSHPort int
	AuthType CredentialAuthType
	Secret CredentialSecret
	TestConnection bool
}

type UpdateServerInput struct {
	Name, Address, Username string
	SSHPort int
	AuthType *CredentialAuthType
	Secret *CredentialSecret
}

type Snapshot struct {
	ID, ServerID string
	OSFamily, OSVersion, KernelVersion, Architecture, Hostname string
	CPUCores int
	MemoryBytes, DiskBytes int64
	Load1 float64
	UptimeSeconds int64
	CollectedAt time.Time
}

type SoftwareItem struct {
	Category, Name, Version, Architecture, Source, Status string
}
```

Create `errors.go`:

```go
package assets

import "errors"

var (
	ErrNotFound = errors.New("asset server not found")
	ErrNameConflict = errors.New("asset server name already exists")
	ErrInvalidInput = errors.New("invalid asset input")
	ErrEncryptionUnavailable = errors.New("asset credential encryption is not configured")
	ErrHostKeyUntrusted = errors.New("ssh host key requires confirmation")
	ErrHostKeyChanged = errors.New("ssh host key changed")
	ErrActiveTask = errors.New("asset server has an active deployment task")
)
```

- [ ] **Step 4: Implement AES-GCM exactly**

Create `cipher.go` using `aes.NewCipher`, `cipher.NewGCM`, `crypto/rand.Read`, and JSON serialization of `CredentialSecret`. The public API must be:

```go
type CredentialCipher interface {
	Encrypt(CredentialSecret) (CredentialEnvelope, error)
	Decrypt(CredentialEnvelope) (CredentialSecret, error)
}

func NewAESGCMCredentialCipher(key []byte) (CredentialCipher, error)
```

Use associated data `[]byte("aurora-aiops/asset-credential/v1")`, set `KeyVersion: 1`, reject any other key version during decrypt, and wrap errors without including serialized plaintext.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/assets -run AESGCM -count=1`

Expected: 3 tests PASS.

Commit:

```bash
git add server/internal/assets
git commit -m "feat(assets): encrypt remote credentials"
```

### Task 3: Implement the asset repository and snapshot retention

**Files:**
- Create: `server/internal/assets/repository.go`
- Create: `server/internal/assets/repository_test.go`

- [ ] **Step 1: Write repository behavior tests**

Create tests that use `store.Open(t.TempDir()+"/assets.db")` and assert these exact behaviors:

```go
func TestRepositoryCreatesPendingServerWithoutConnecting(t *testing.T) {
	repo := newAssetRepositoryTest(t)
	created, err := repo.CreateServer(context.Background(), Server{
		ID: "s1", Name: "edge-1", Address: "10.0.0.8", SSHPort: 22,
		Username: "root", CredentialID: "c1", Status: ServerPending,
	}, storedCredentialFixture("c1"))
	if err != nil { t.Fatal(err) }
	if created.Status != ServerPending { t.Fatalf("status=%s", created.Status) }
}
```

Add four more complete tests with these exact assertions:

- `TestRepositoryRejectsDuplicateName` creates `edge-1` twice and checks `errors.Is(err, ErrNameConflict)`.
- `TestRepositoryNeverReturnsCredentialCiphertextFromServerQueries` stores a known ciphertext byte sequence, calls both `GetServer` and `ListServers`, JSON-marshals the returned `Server` values, and proves the known sequence and credential envelope are absent.
- `TestRepositoryKeepsLatestThirtySnapshots` saves 31 timestamped snapshots, checks `COUNT(*) = 30`, and proves snapshot 1 was deleted while snapshot 31 is returned by `LatestSnapshot`.
- `TestRepositoryCollectionFailureDoesNotReplaceLatestSnapshot` saves one successful snapshot, calls `MarkCollectionFailure`, and proves `LatestSnapshot.ID` is unchanged while server status and message reflect the failure.

- [ ] **Step 2: Run and verify RED**

Run: `cd server && go test ./internal/assets -run Repository -count=1`

Expected: compile failure because `Repository` and persistence methods are missing.

- [ ] **Step 3: Implement repository contracts**

The concrete repository must expose:

```go
type Repository struct { db *sql.DB }
func NewRepository(db *sql.DB) *Repository
func (r *Repository) CreateServer(ctx context.Context, server Server, credential StoredCredential) (Server, error)
func (r *Repository) UpdateServer(ctx context.Context, server Server, credential *StoredCredential) (Server, error)
func (r *Repository) GetServer(ctx context.Context, id string) (Server, error)
func (r *Repository) ListServers(ctx context.Context) ([]Server, error)
func (r *Repository) DeleteServer(ctx context.Context, id string) error
func (r *Repository) GetCredential(ctx context.Context, id string) (StoredCredential, error)
func (r *Repository) ConfirmHostKey(ctx context.Context, id, fingerprint string, now time.Time) error
func (r *Repository) SaveCollection(ctx context.Context, server Server, snapshot Snapshot, software []SoftwareItem) error
func (r *Repository) MarkCollectionFailure(ctx context.Context, id string, status ServerStatus, message string, now time.Time) error
func (r *Repository) LatestSnapshot(ctx context.Context, serverID string) (Snapshot, error)
func (r *Repository) ListLatestSoftware(ctx context.Context, serverID string) ([]SoftwareItem, error)
```

Define `StoredCredential` in `model.go` with ID, auth type, envelope and timestamps. `CreateServer`, `UpdateServer`, `SaveCollection`, and deletion must use transactions. Translate SQLite unique errors on `asset_servers.name` to `ErrNameConflict`. `SaveCollection` inserts snapshot and software, updates server summary, and deletes snapshots beyond the latest 30 in the same transaction.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/assets -run Repository -count=1`

Expected: repository tests PASS.

Commit:

```bash
git add server/internal/assets
git commit -m "feat(assets): persist servers and inventory snapshots"
```

### Task 4: Add verified SSH transport

**Files:**
- Create: `server/internal/assets/ssh.go`
- Create: `server/internal/assets/ssh_test.go`

- [ ] **Step 1: Write transport contract tests with an in-process SSH server**

Tests must start a loopback `net.Listener`, use a generated Ed25519 host key, and configure `ssh.ServerConfig` for password and public-key authentication. Assert:

```go
func TestSSHTransportReportsUntrustedFingerprint(t *testing.T)
func TestSSHTransportRejectsChangedFingerprint(t *testing.T)
func TestSSHTransportRunsBoundedCommandWithPassword(t *testing.T)
func TestSSHTransportRunsBoundedCommandWithPrivateKey(t *testing.T)
func TestSSHTransportCancelsCommand(t *testing.T)
func TestSSHTransportTruncatesOutputAtConfiguredLimit(t *testing.T)
```

`TestSSHTransportReportsUntrustedFingerprint` must use an empty expected fingerprint and assert `errors.Is(err, ErrHostKeyUntrusted)` plus a non-empty fingerprint in `HostKeyError`.

- [ ] **Step 2: Run and verify RED**

Run: `cd server && go test ./internal/assets -run SSHTransport -count=1`

Expected: compile failure because `SSHTransport` is missing.

- [ ] **Step 3: Implement the transport**

Define:

```go
type RemoteTarget struct { Address string; Port int; Username, ExpectedFingerprint string }
type CommandResult struct { Stdout, Stderr string; ExitCode int; Truncated bool }
type HostKeyError struct { Expected, Actual string; Changed bool }
type RemoteTransport interface {
	ProbeHostKey(context.Context, RemoteTarget) (string, error)
	Run(context.Context, RemoteTarget, CredentialSecret, string, int64) (CommandResult, error)
	Upload(context.Context, RemoteTarget, CredentialSecret, io.Reader, int64, string, fs.FileMode) error
}
```

Implement `SSHTransport` with `ssh.Dial`-equivalent context-aware TCP dialing, `ssh.NewClientConn`, password/private-key auth, SHA256 fingerprints, a 10-second dial timeout, per-call context cancellation, and bounded stdout/stderr writers. `Run` receives commands only from internal collectors/installers; routes must never accept this string.

Implement upload with an SSH session running `install -m <mode> /dev/stdin <quoted-path>` and a strict POSIX single-quote helper. Reject paths outside `/tmp/aurora-aiops/` and `/opt/aurora-aiops/`.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/assets -run SSHTransport -count=1`

Expected: all SSH tests PASS without external network access.

Commit:

```bash
git add server/internal/assets/ssh.go server/internal/assets/ssh_test.go
git commit -m "feat(assets): add verified ssh transport"
```

### Task 5: Collect and normalize host inventory

**Files:**
- Create: `server/internal/assets/collector.go`
- Create: `server/internal/assets/collector_test.go`

- [ ] **Step 1: Write parser and orchestration tests**

Use a fake `RemoteTransport` keyed by exact command string. Test Ubuntu `dpkg-query`, RHEL `rpm -qa`, Alpine `apk info -v`, systemd services, container runtime versions, Kubernetes tools, malformed numeric values, and one failed command. The orchestration test must assert that all commands are compile-time constants and no field from `Server` is interpolated into shell text.

The central assertion shape is:

```go
snapshot, items, err := collector.Collect(ctx, server, secret)
if err != nil { t.Fatal(err) }
if snapshot.OSFamily != "debian" || snapshot.Architecture != "amd64" { t.Fatalf("snapshot=%+v", snapshot) }
if !containsSoftware(items, "package", "curl", "8.5.0") { t.Fatalf("items=%+v", items) }
```

- [ ] **Step 2: Run and verify RED**

Run: `cd server && go test ./internal/assets -run Collector -count=1`

Expected: compile failure because `Collector` is missing.

- [ ] **Step 3: Implement the fixed command collector**

Implement:

```go
type Collector struct { remote RemoteTransport; now func() time.Time }
func NewCollector(remote RemoteTransport, now func() time.Time) *Collector
func (c *Collector) Collect(context.Context, Server, CredentialSecret) (Snapshot, []SoftwareItem, error)
```

Use exact fixed commands with `LC_ALL=C`; parse `/etc/os-release`, `uname -m`, `/proc/meminfo`, `df -B1 /`, `/proc/loadavg`, `/proc/uptime`, package manager output, `systemctl list-unit-files`, and version commands. Normalize `x86_64` to `amd64`, `aarch64` to `arm64`, and package categories to `package`, `runtime`, `kubernetes`, `service`, `aurora`.

Treat the base system probe as required. Treat optional package/runtime commands as warnings represented by a `SoftwareItem{Category:"collector_warning"}` so one unavailable package manager does not fail the snapshot.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/assets -run Collector -count=1`

Expected: collector tests PASS.

Commit:

```bash
git add server/internal/assets/collector.go server/internal/assets/collector_test.go
git commit -m "feat(assets): collect host and software inventory"
```

### Task 6: Add the asset service, audit records and HTTP routes

**Files:**
- Create: `server/internal/assets/service.go`
- Create: `server/internal/assets/service_test.go`
- Create: `server/internal/server/asset_routes.go`
- Create: `server/internal/server/asset_routes_test.go`
- Modify: `server/internal/server/server.go`
- Modify: `server/internal/server/router.go`
- Modify: `server/internal/server/platform_rbac.go`
- Modify: `server/internal/server/platform_rbac_test.go`
- Modify: `server/internal/server/platform_rbac_integration_test.go`

- [ ] **Step 1: Write service tests first**

Cover these public operations with a fake repository/remote and real cipher:

```go
func TestServiceCreatesPendingServerWithoutSSH(t *testing.T)
func TestServiceCreateWithTestReturnsHostKeyConfirmation(t *testing.T)
func TestServiceConfirmHostKeyAuditsFingerprintOnly(t *testing.T)
func TestServiceCollectDecryptsCredentialAndPersistsSnapshot(t *testing.T)
func TestServiceCollectFailureKeepsPreviousSnapshot(t *testing.T)
func TestServiceAuditPayloadNeverContainsCredential(t *testing.T)
func TestServiceRejectsMissingEncryptionKeyForCredentialMutation(t *testing.T)
```

The first test must assert zero fake-remote calls when `TestConnection` is false.

- [ ] **Step 2: Run service tests and verify RED**

Run: `cd server && go test ./internal/assets -run Service -count=1`

Expected: compile failure because `Service` is missing.

- [ ] **Step 3: Implement service orchestration**

Expose:

```go
func NewService(repo *Repository, cipher CredentialCipher, remote RemoteTransport, collector *Collector, audit audit.Repository, now func() time.Time) *Service
func (s *Service) Create(context.Context, string, CreateServerInput) (Server, error)
func (s *Service) Update(context.Context, string, string, UpdateServerInput) (Server, error)
func (s *Service) Delete(context.Context, string, string) error
func (s *Service) List(context.Context) ([]Server, error)
func (s *Service) Get(context.Context, string) (Server, error)
func (s *Service) TestConnection(context.Context, string, string) (ConnectionResult, error)
func (s *Service) ConfirmHostKey(context.Context, string, string, string) error
func (s *Service) Collect(context.Context, string, string) (Snapshot, []SoftwareItem, error)
func (s *Service) LatestSnapshot(context.Context, string) (Snapshot, error)
func (s *Service) Software(context.Context, string) ([]SoftwareItem, error)
```

Validate name/address/username/control characters, normalize default port to 22, use UUIDs, encrypt before repository calls, and audit only actor/action/server ID/result/auth type/fingerprint. Return a typed `HostKeyError` through service unchanged.

- [ ] **Step 4: Write route and RBAC tests**

Add route tests for create-without-test (`201`, `pending`), validation (`400 INVALID_ARGUMENT`), not found (`404 ASSET_NOT_FOUND`), host-key confirmation (`409 SSH_HOST_KEY_CONFIRMATION_REQUIRED`), and list returning `[]`.

Extend `TestRequiredPlatformRoles` with:

```go
{http.MethodGet, "/api/v1/assets/servers", nil},
{http.MethodPost, "/api/v1/assets/servers/:id/test-connection", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
{http.MethodPost, "/api/v1/assets/servers/:id/collect", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
{http.MethodPost, "/api/v1/assets/servers", []auth.Role{auth.RoleAdmin}},
{http.MethodPatch, "/api/v1/assets/servers/:id", []auth.Role{auth.RoleAdmin}},
{http.MethodDelete, "/api/v1/assets/servers/:id", []auth.Role{auth.RoleAdmin}},
{http.MethodPost, "/api/v1/assets/servers/:id/confirm-host-key", []auth.Role{auth.RoleAdmin}},
```

- [ ] **Step 5: Run route tests and verify RED**

Run: `cd server && go test ./internal/server -run 'Asset|RequiredPlatformRoles|ClassifiesEveryRegisteredRoute' -count=1`

Expected: role expectations or route requests fail because routes are not registered.

- [ ] **Step 6: Implement and register routes**

Create `asset_routes.go` with request DTOs whose credential fields use `json:"password,omitempty"`, `json:"privateKey,omitempty"`, and `json:"passphrase,omitempty"`, but response DTOs expose only `credentialAuthType` and `credentialConfigured`.

Register all Phase-1 asset endpoints from the approved spec under the authorized group. Map sentinel errors to stable response codes. Add `assetService *assets.Service` to `newRouter`, construct it in `server.Run`, and update all test router constructors.

Add only test/collect paths to `operatorWritePaths`; every other unsafe route stays admin-by-default. Add integration matrix cases proving viewer list 200, viewer collect 403, operator collect reaches handler, operator create 403, and admin create 201.

- [ ] **Step 7: Verify GREEN and commit**

Run: `cd server && go test ./internal/assets ./internal/server -count=1`

Expected: PASS.

Commit:

```bash
git add server/internal/assets server/internal/server
git commit -m "feat(assets): expose audited asset APIs"
```

### Task 7: Add frontend API, navigation and asset pages

**Files:**
- Create: `web/src/modules/assets/types.ts`
- Create: `web/src/modules/assets/api.ts`
- Create: `web/src/modules/assets/api.test.ts`
- Create: `web/src/modules/assets/components/AddServerDrawer.tsx`
- Create: `web/src/modules/assets/components/AddServerDrawer.test.tsx`
- Create: `web/src/pages/AssetServersPage.tsx`
- Create: `web/src/pages/AssetServersPage.test.tsx`
- Create: `web/src/pages/AssetServerDetailsPage.tsx`
- Create: `web/src/pages/AssetServerDetailsPage.test.tsx`
- Modify: `web/src/layouts/navigation.tsx`
- Modify: `web/src/layouts/navigation.test.tsx`
- Modify: `web/src/router/AppRouter.tsx`

- [ ] **Step 1: Define API types and write failing API tests**

Define `AssetServer`, `AssetSnapshot`, `AssetSoftwareItem`, `CreateAssetServerInput`, and `ConnectionResult` with camelCase fields matching route responses. Create `api.test.ts` using `axios-mock-adapter` or the repository's existing Axios mocking pattern and assert exact URLs/methods for list/create/get/collect/software.

Required functions:

```ts
export const listAssetServers = async (): Promise<AssetServer[]>;
export const createAssetServer = async (input: CreateAssetServerInput): Promise<AssetServer>;
export const getAssetServer = async (id: string): Promise<AssetServer>;
export const testAssetConnection = async (id: string): Promise<ConnectionResult>;
export const confirmAssetHostKey = async (id: string, fingerprint: string): Promise<void>;
export const collectAssetServer = async (id: string): Promise<AssetSnapshot>;
export const getLatestAssetSnapshot = async (id: string): Promise<AssetSnapshot>;
export const listAssetSoftware = async (id: string): Promise<AssetSoftwareItem[]>;
```

- [ ] **Step 2: Verify API tests RED, implement, then GREEN**

Run before implementation: `cd web && npm test -- src/modules/assets/api.test.ts`

Expected: module-not-found failure.

Implement functions with the existing `http` envelope pattern, then rerun. Expected: PASS.

- [ ] **Step 3: Write the AddServerDrawer behavior tests**

Tests must assert:

- `submits a pending server when immediate test is disabled`: fill name/address/user/password, disable the switch, submit, and assert the API input includes `testConnection: false` and the returned `pending` row remains visible.
- `never renders a saved credential value when reopened`: close and reopen after creation and assert password, private-key and passphrase controls have empty values.
- `switches between password and private-key fields`: select each auth type and assert only its exact credential labels are visible.
- `surfaces host-key confirmation without losing the created server`: return the typed confirmation response, assert the fingerprint is rendered, cancel confirmation, and prove the created server is still in the list.

- [ ] **Step 4: Run drawer tests RED, implement, then GREEN**

Run before: `cd web && npm test -- src/modules/assets/components/AddServerDrawer.test.tsx`

Expected: module-not-found failure.

Implement with Ant Design `Drawer`, `Form`, `Input`, `Input.Password`, `Radio`, `InputNumber`, and `Switch`. Default port is 22 and immediate test is true. On success invalidate `['asset-servers']`; if a host-key confirmation is returned, show a confirmation modal with the fingerprint and call the confirm endpoint only after explicit admin action.

- [ ] **Step 5: Write list/detail/navigation tests**

List tests assert metric counts, “新增服务器”, pending/offline tags, search, row navigation and disabled mutation controls in demo/viewer states. Detail tests assert the four tabs and stale snapshot timestamp after a collection failure. Navigation tests assert `/assets/servers` under section label `资产管理`; the project-center entry is added with its real page in Phase 3.

- [ ] **Step 6: Run page tests RED, implement, then GREEN**

Run before: `cd web && npm test -- src/pages/AssetServersPage.test.tsx src/pages/AssetServerDetailsPage.test.tsx src/layouts/navigation.test.tsx`

Expected: missing pages/routes/navigation assertions fail.

Implement `AssetServersPage` with `ResourceListPage`, status metrics and row navigation. Implement details with Ant Design `Tabs`, `Descriptions`, `Progress`, and `Table`; installations/tasks tabs render explicit “部署任务功能将在下一阶段启用” empty states and make no unavailable API calls. Add the server route and navigation item with `CloudServerOutlined`.

- [ ] **Step 7: Verify frontend GREEN and commit**

Run:

```bash
cd web
npm test
npm run build
```

Expected: all tests and TypeScript build PASS.

Commit:

```bash
git add web/src/modules/assets web/src/pages/AssetServersPage.tsx web/src/pages/AssetServersPage.test.tsx web/src/pages/AssetServerDetailsPage.tsx web/src/pages/AssetServerDetailsPage.test.tsx web/src/layouts/navigation.tsx web/src/layouts/navigation.test.tsx web/src/router/AppRouter.tsx
git commit -m "feat(web): add asset inventory pages"
```

### Task 8: Phase-1 verification and documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/aiops/api-v1.md`
- Create: `docs/aiops/acceptance/2026-08-09-asset-inventory-ssh.md`

- [ ] **Step 1: Document configuration and API**

Document `AURORA_AIOPS_ASSET_ENCRYPTION_KEY`, collection interval, supported SSH auth, host-key confirmation, offline create semantics, API routes and RBAC. Include the exact key command:

```bash
openssl rand -base64 32
```

State that the key must come from a secret manager and must not be committed.

- [ ] **Step 2: Run full verification**

Run:

```bash
cd server && gofmt -w internal/assets internal/server internal/config internal/store
cd server && go test ./...
cd server && go vet ./...
cd web && npm test
cd web && npm run build
./scripts/verify-brand-rename.sh
git diff --check
git grep -In -E '(BEGIN (RSA|OPENSSH) PRIVATE KEY|password[=:][^*]|passphrase[=:][^*])' -- server web docs ':!**/*_test.go'
```

Expected: all commands exit 0; secret scan produces no matches.

- [ ] **Step 3: Record acceptance evidence and commit**

The acceptance file must record command, exit code, test counts, supported OS scope, and that the SSH integration test used loopback only. Do not claim real-host acceptance in Phase 1.

Commit:

```bash
git add README.md docs/aiops
git commit -m "docs: record asset inventory acceptance"
```
