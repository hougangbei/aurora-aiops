# Durable Deployment Tasks and Progress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a crash-resilient deployment task engine with one active task per server, durable step/event history, SSE replay, cancellation/retry, audit records, and resumable progress UI.

**Architecture:** A new `server/internal/deployment` package owns catalog metadata, task persistence, leases, redaction, execution and event fan-out. Installers supply fixed `StepDefinition` plans; the worker owns state transitions and never accepts shell commands from HTTP. SQLite is authoritative, while in-memory subscriptions only wake connected SSE clients. The React client resumes from persisted event IDs and falls back to polling.

**Tech Stack:** Go 1.25, Gin, SQLite, Server-Sent Events, React 19, TypeScript, TanStack Query, Ant Design, Vitest.

---

## Stable contracts

Use these values in persistence, JSON and tests:

```go
type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type TaskAction string

const (
	TaskActionInstall TaskAction = "install"
	TaskActionAdopt   TaskAction = "adopt"
)

type Task struct {
	ID, ServerID, ProjectID, Version, Actor, RetryOf string
	Action TaskAction
	Status TaskStatus
	CurrentStepID, CurrentStepLabel, ErrorCode, ErrorMessage string
	Percent int
	CancelRequested bool
	CreatedAt, UpdatedAt time.Time
	StartedAt, FinishedAt *time.Time
}

type Project struct {
	ID, Name, Description string
	Versions []string
	SupportedOSFamilies, SupportedArchitectures []string
}

type StepDefinition struct {
	ID, Label string
	Percent int
	Timeout time.Duration
	Probe func(context.Context, ExecutionContext) (bool, error)
	Run func(context.Context, ExecutionContext) error
}

type Installer interface {
	Project() Project
	NormalizeConfiguration(json.RawMessage) (json.RawMessage, error)
	BuildPlan(Task, assets.Server, json.RawMessage) ([]StepDefinition, error)
}
```

`Percent` is the completed percentage after a step, strictly increasing and ending at 100. A successful `Probe` skips `Run` but still completes the step. Installer output reaches persistence only through the redacting logger in `ExecutionContext`.

### Task 1: Add the durable task schema

**Files:**
- Modify: `server/internal/store/migrate.go`
- Modify: `server/internal/store/migrate_test.go`

- [ ] **Step 1: Write failing migration tests**

Add `TestOpenCreatesDeploymentSchema` and assert that `deployment_tasks`, `deployment_steps`, `deployment_events` and `deployment_task_values` exist. Query `sqlite_master` and assert a unique partial index named `idx_deployment_tasks_one_active_server` exists with predicate `status IN ('queued','running')`.

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/store -run TestOpenCreatesDeploymentSchema -count=1`

Expected: FAIL because the tables are absent.

- [ ] **Step 3: Add the schema**

Append this migration in the existing transaction:

```sql
CREATE TABLE IF NOT EXISTS deployment_tasks (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  version TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('install','adopt')),
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
  actor TEXT NOT NULL,
  retry_of TEXT NOT NULL DEFAULT '',
  current_step_id TEXT NOT NULL DEFAULT '',
  current_step_label TEXT NOT NULL DEFAULT '',
  percent INTEGER NOT NULL DEFAULT 0 CHECK (percent BETWEEN 0 AND 100),
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  config_nonce BLOB NOT NULL,
  config_ciphertext BLOB NOT NULL,
  config_key_version INTEGER NOT NULL DEFAULT 1,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(retry_of) REFERENCES deployment_tasks(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployment_tasks_one_active_server
ON deployment_tasks(server_id) WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS idx_deployment_tasks_status_created
ON deployment_tasks(status, created_at);
CREATE TABLE IF NOT EXISTS deployment_steps (
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  step_id TEXT NOT NULL,
  label TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  percent INTEGER NOT NULL CHECK (percent BETWEEN 1 AND 100),
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped','cancelled')),
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(task_id, step_id),
  UNIQUE(task_id, ordinal)
);
CREATE TABLE IF NOT EXISTS deployment_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deployment_events_task_id
ON deployment_events(task_id, id);
CREATE TABLE IF NOT EXISTS deployment_task_values (
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  value_key TEXT NOT NULL,
  value_text TEXT NOT NULL,
  PRIMARY KEY(task_id, value_key)
);
```

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/store -count=1`

Expected: PASS.

```bash
git add server/internal/store/migrate.go server/internal/store/migrate_test.go
git commit -m "feat(deployment): add durable task schema"
```

### Task 2: Implement task models, catalog and repository state rules

**Files:**
- Create: `server/internal/deployment/model.go`
- Create: `server/internal/deployment/errors.go`
- Create: `server/internal/deployment/catalog.go`
- Create: `server/internal/deployment/catalog_test.go`
- Create: `server/internal/deployment/secret.go`
- Create: `server/internal/deployment/secret_test.go`
- Create: `server/internal/deployment/repository.go`
- Create: `server/internal/deployment/repository_test.go`

- [ ] **Step 1: Write failing catalog and repository tests**

Tests must prove:

- catalog registration rejects an empty ID, duplicate ID, unsorted/duplicate versions and a project with no supported architecture;
- the task-config cipher requires the configured 32-byte asset key, authenticates task ID as associated data, rejects tampering, and never returns plaintext through task reads;
- `CreateTask` persists all steps atomically and rejects a second queued/running task for the same server with `ErrActiveTask`;
- `ClaimNext` changes queued to running, records owner/lease/start time and returns the oldest task;
- `CompleteStep` rejects a percentage lower than the task's current percentage and rejects updates after a terminal status;
- `RequestCancel` sets the flag but does not falsely mark a running task cancelled;
- `Finish` writes exactly one terminal state and terminal tasks are immutable;
- `RecoverExpired` returns expired running tasks to queued without losing completed steps;
- `Retry` creates a new queued task with a new ID and `retry_of`, and rejects retrying a non-terminal task.
- `PutValue` accepts only keys registered by the installer, `GetValue` resumes them after reopening SQLite, and task/API JSON never includes them.

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/deployment -run 'Catalog|Repository' -count=1`

Expected: package or symbols do not exist.

- [ ] **Step 3: Implement stable errors and catalog**

Define `ErrNotFound`, `ErrActiveTask`, `ErrInvalidTransition`, `ErrUnknownProject`, `ErrUnsupportedTarget` and `ErrNotRetryable`. Implement `Catalog.Register`, `Catalog.Get`, and `Catalog.List`; return copied/sorted project slices so callers cannot mutate registrations.

Implement `SecretCipher.Seal(scope, resourceID string, plaintext []byte) (SealedSecret, error)` and `SecretCipher.Open(scope, resourceID string, sealed SealedSecret) ([]byte, error)` with AES-256-GCM and associated data `aurora-aiops/deployment-secret/v1\x00<scope>\x00<resourceID>`. The `task-config` scope encrypts the installer's normalized JSON configuration before task creation. Reuse `AURORA_AIOPS_ASSET_ENCRYPTION_KEY`; missing encryption disables deployment mutations but does not disable asset reads.

- [ ] **Step 4: Implement transactional repository methods**

Expose:

```go
func NewRepository(db *sql.DB, now func() time.Time) *Repository
func (r *Repository) CreateTask(context.Context, Task, SealedSecret, []StepDefinition) (Task, error)
func (r *Repository) GetTask(context.Context, string) (Task, error)
func (r *Repository) OpenTaskConfiguration(context.Context, string, SecretCipher) (json.RawMessage, error)
func (r *Repository) PutValue(context.Context, string, string, string) error
func (r *Repository) GetValue(context.Context, string, string) (string, bool, error)
func (r *Repository) ListServerTasks(context.Context, string) ([]Task, error)
func (r *Repository) ListSteps(context.Context, string) ([]Step, error)
func (r *Repository) ClaimNext(context.Context, string, time.Duration) (Task, bool, error)
func (r *Repository) RenewLease(context.Context, string, string, time.Duration) error
func (r *Repository) StartStep(context.Context, string, string, string, EventInput) error
func (r *Repository) CompleteStep(context.Context, string, string, bool, int, EventInput) error
func (r *Repository) FailStep(context.Context, string, string, string, EventInput) error
func (r *Repository) RequestCancel(context.Context, string, EventInput) error
func (r *Repository) Finish(context.Context, string, TaskStatus, string, string, EventInput) error
func (r *Repository) RecoverExpired(context.Context) (int64, error)
func (r *Repository) Retry(context.Context, string, string, string, []StepDefinition) (Task, error)
```

Use `BEGIN IMMEDIATE` semantics for claim/create transitions, translate the partial-index violation to `ErrActiveTask`, and update rows with an expected current status in every state-changing `WHERE` clause. `CreateTask` writes its queued event in the creation transaction; each step/task transition writes its supplied `EventInput` in the same transaction. Never overwrite completed step rows during recovery. `GetTask`, list methods, API DTOs, events and audit records must not expose the encrypted configuration columns.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment -run 'Catalog|Repository' -count=1`

Expected: PASS.

```bash
git add server/internal/deployment/model.go server/internal/deployment/errors.go server/internal/deployment/catalog.go server/internal/deployment/catalog_test.go server/internal/deployment/secret.go server/internal/deployment/secret_test.go server/internal/deployment/repository.go server/internal/deployment/repository_test.go
git commit -m "feat(deployment): enforce durable task transitions"
```

### Task 3: Persist replayable events and redact logs

**Files:**
- Create: `server/internal/deployment/events.go`
- Create: `server/internal/deployment/events_test.go`
- Create: `server/internal/deployment/redactor.go`
- Create: `server/internal/deployment/redactor_test.go`

- [ ] **Step 1: Write failing event-store tests**

Assert `Append` assigns increasing integer IDs, `ListAfter(taskID, id)` returns only newer events in order, subscriptions receive a wake-up after commit, a slow subscriber cannot block a writer, and closing a subscription removes it. Reopen SQLite and prove events still replay.

- [ ] **Step 2: Write failing redaction tests**

Create a redactor from secrets `p@ss`, a PEM private key, `token=abc123`, and a multiline kubeconfig. Assert exact secrets, URL-encoded secrets, authorization headers, `password=`, `passphrase=`, `token=`, and PEM bodies become `[REDACTED]`; normal progress text remains unchanged. Add a 16 KiB output limit test.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment -run 'Event|Redact' -count=1`

Expected: missing APIs.

- [ ] **Step 4: Implement event and redaction contracts**

Expose `EventStore.Append`, `ListAfter`, and `Subscribe`. `Append` is for redacted informational log events and writes JSON payloads to SQLite before non-blocking subscriber notification; state transition events are inserted by repository transactions and use the same subscriber notifier after commit. Expose `NewRedactingLogger(secrets ...string)` with `Info(message string)` and `Output() string`; redact before truncation and before any database/audit/logger call.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment -run 'Event|Redact' -count=1`

Expected: PASS.

```bash
git add server/internal/deployment/events.go server/internal/deployment/events_test.go server/internal/deployment/redactor.go server/internal/deployment/redactor_test.go
git commit -m "feat(deployment): persist redacted progress events"
```

### Task 4: Implement the idempotent worker

**Files:**
- Create: `server/internal/deployment/worker.go`
- Create: `server/internal/deployment/worker_test.go`

- [ ] **Step 1: Write worker tests with fake installers**

Cover these deterministic cases: successful three-step task; probe-skipped step; run failure; timeout; cancellation between steps; cancellation during a command context; panic converted to `failed/INSTALLER_PANIC`; lease renewal; process restart recovery; monotonic percentages; no secret in events or task errors. Inject a transaction failure and prove neither the transition nor its event commits; on success assert both commit in the same order.

- [ ] **Step 2: Verify RED**

Run: `cd server && go test ./internal/deployment -run Worker -count=1`

Expected: `Worker` is undefined.

- [ ] **Step 3: Implement worker lifecycle**

`Worker.Start(ctx)` first calls `RecoverExpired`, then claims at most one task per poll. Resolve the installer from the catalog, decrypt the normalized task configuration, rebuild the deterministic plan, load an execution context through a narrow `TargetProvider`, and execute only incomplete steps. Seed redaction with every scalar string found in the configuration before the first log. Wrap every probe/run in its timeout context, renew the lease at one-third of lease duration, check `CancelRequested` before each step, and finish with one of the five stable statuses.

Define `TargetProvider.ExecutionContext(ctx, serverID, taskID)` returning the public server and an `ExecutionContext` whose methods close over the decrypted SSH credential; do not return that credential or task configuration in task/event types. Start with one worker goroutine so the single-active-server guarantee remains simple and deterministic.

- [ ] **Step 4: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment -run Worker -count=1`

Expected: PASS under `-race` as well.

```bash
cd server && go test -race ./internal/deployment -run Worker -count=1
git add server/internal/deployment/worker.go server/internal/deployment/worker_test.go
git commit -m "feat(deployment): execute resumable installer steps"
```

### Task 5: Add task/project APIs, SSE and RBAC

**Files:**
- Create: `server/internal/deployment/service.go`
- Create: `server/internal/deployment/service_test.go`
- Create: `server/internal/server/deployment_routes.go`
- Create: `server/internal/server/deployment_routes_test.go`
- Modify: `server/internal/server/server.go`
- Modify: `server/internal/server/router.go`
- Modify: `server/internal/server/platform_rbac.go`
- Modify: `server/internal/server/platform_rbac_test.go`
- Modify: `server/internal/server/platform_rbac_integration_test.go`

- [ ] **Step 1: Write service tests**

Assert catalog list/get, install validation, unsupported target errors, active-task conflict, cancel, retry, task/step reads, server task list and installation list. Prove audit records include actor/task/server/project/action/result but no credential or event log.

- [ ] **Step 2: Write route and SSE tests**

Cover these routes:

```text
GET  /api/v1/projects
GET  /api/v1/projects/:projectID
POST /api/v1/projects/:projectID/install
GET  /api/v1/assets/servers/:serverID/tasks
GET  /api/v1/assets/servers/:serverID/installations
GET  /api/v1/deployment-tasks/:taskID
GET  /api/v1/deployment-tasks/:taskID/events
POST /api/v1/deployment-tasks/:taskID/cancel
POST /api/v1/deployment-tasks/:taskID/retry
```

The install body is `{serverId, version, configuration?}`. Routes pass `configuration` as raw JSON only to the selected built-in installer's strict normalizer, seal the normalized result, then discard the request bytes. Unknown fields or installer-specific fields with wrong types fail with `400 INVALID_ARGUMENT`. SSE tests send `Last-Event-ID`, assert replay starts after it, assert frames contain `id`, `event`, and JSON `data`, assert a heartbeat comment within 15 seconds using a fake clock, and assert terminal tasks close after replay. A query parameter `lastEventId` is supported for clients that cannot set headers.

- [ ] **Step 3: Verify RED**

Run: `cd server && go test ./internal/deployment ./internal/server -run 'Deployment|Project|RequiredPlatformRoles|ClassifiesEveryRegisteredRoute' -count=1`

Expected: routes and service are missing.

- [ ] **Step 4: Implement routes and authorization**

Viewer may use all GET routes. Install/cancel/retry are admin-only. Keep SSE under session and RBAC middleware. Map `ErrActiveTask` to `409 DEPLOYMENT_ACTIVE_TASK`, unsupported targets to `422 DEPLOYMENT_UNSUPPORTED_TARGET`, and immutable/invalid transitions to `409 DEPLOYMENT_INVALID_TRANSITION`.

Construct the service and worker in `server.Run`, pass the service into `newRouter`, start the worker with the server lifecycle context, and wait for it during graceful shutdown. Update every router test constructor and registered-route classifier.

- [ ] **Step 5: Verify GREEN and commit**

Run: `cd server && go test ./internal/deployment ./internal/server -count=1`

Expected: PASS.

```bash
git add server/internal/deployment server/internal/server
git commit -m "feat(deployment): expose task APIs and event stream"
```

### Task 6: Build resumable progress UI

**Files:**
- Create: `web/src/modules/deployment/types.ts`
- Create: `web/src/modules/deployment/api.ts`
- Create: `web/src/modules/deployment/api.test.ts`
- Create: `web/src/modules/deployment/useDeploymentEvents.ts`
- Create: `web/src/modules/deployment/useDeploymentEvents.test.tsx`
- Create: `web/src/modules/deployment/components/TaskProgressDrawer.tsx`
- Create: `web/src/modules/deployment/components/TaskProgressDrawer.test.tsx`
- Create: `web/src/modules/deployment/components/GlobalTaskIndicator.tsx`
- Create: `web/src/modules/deployment/components/GlobalTaskIndicator.test.tsx`
- Modify: `web/src/pages/AssetServerDetailsPage.tsx`
- Modify: `web/src/pages/AssetServerDetailsPage.test.tsx`
- Modify: `web/src/layouts/AppLayout.tsx`

- [ ] **Step 1: Write API and event-hook tests**

Assert exact task/list/cancel/retry URLs. With fake `EventSource`, prove the hook resumes from the last event ID, ignores duplicate/out-of-order IDs, never lowers percent, reconnects with bounded exponential delays, switches to two-second polling after three SSE failures, stops after a terminal state, and performs one final task fetch.

- [ ] **Step 2: Verify RED, implement API/hook, verify GREEN**

Run before: `cd web && npm test -- src/modules/deployment/api.test.ts src/modules/deployment/useDeploymentEvents.test.tsx`

Expected: modules are missing.

Use existing `http` response envelopes. Because native `EventSource` cannot add `Last-Event-ID`, reconnect with `?lastEventId=<lastEventID>`. Keep the latest task in TanStack Query under `['deployment-task', id]`.

Run after implementation with the same command. Expected: PASS.

- [ ] **Step 3: Write progress component tests**

Assert step labels/status icons, monotonic progress bar, bounded redacted log rendering, reconnect indicator, admin-only cancel/retry buttons, terminal success/failure states, and drawer restoration when `task` is present in the URL query string. Assert the global indicator links to the currently active task and is hidden when none exists.

- [ ] **Step 4: Implement components and server-detail tabs**

Render task history in the details `tasks` tab and installation records in `installations`. Opening a task updates `?task=<id>`. `TaskProgressDrawer` fetches persisted steps before subscribing, so refresh never starts blank. Add `GlobalTaskIndicator` to `AppLayout` and source it from active tasks already returned for visited servers; do not add an unbounded global polling endpoint.

- [ ] **Step 5: Verify frontend and commit**

Run:

```bash
cd web
npm test
npm run build
```

Expected: PASS.

```bash
git add web/src/modules/deployment web/src/pages/AssetServerDetailsPage.tsx web/src/pages/AssetServerDetailsPage.test.tsx web/src/layouts/AppLayout.tsx
git commit -m "feat(web): show durable deployment progress"
```

### Task 7: Phase-2 verification and operations documentation

**Files:**
- Modify: `docs/aiops/api-v1.md`
- Create: `docs/aiops/operations/deployment-task-recovery.md`
- Create: `docs/aiops/acceptance/2026-08-09-deployment-task-progress.md`

- [ ] **Step 1: Document API and recovery semantics**

Document task states, one-active-task conflict, event replay, polling fallback, cancellation boundaries, retry linkage, lease recovery, retention and admin/operator/viewer permissions. State that cancelling does not roll back remote changes and re-running relies on probes.

- [ ] **Step 2: Run the full phase gate**

```bash
cd server && gofmt -w internal/deployment internal/server internal/store
cd server && go test -race ./internal/deployment ./internal/server
cd server && go test ./...
cd server && go vet ./...
cd web && npm test
cd web && npm run build
git diff --check
git grep -In -E '(BEGIN (RSA|OPENSSH) PRIVATE KEY|password[=:][^*]|passphrase[=:][^*]|token[=:][^*])' -- server web docs ':!**/*_test.go'
```

Expected: all commands exit 0 and the secret scan returns no matches.

- [ ] **Step 3: Record evidence and commit**

Record command exit codes, test counts, SQLite restart-replay result, worker recovery result, SSE reconnect result and polling fallback result.

```bash
git add docs/aiops/api-v1.md docs/aiops/operations/deployment-task-recovery.md docs/aiops/acceptance/2026-08-09-deployment-task-progress.md
git commit -m "docs: record deployment task recovery acceptance"
```
