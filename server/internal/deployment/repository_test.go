package deployment

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func TestRepositoryCreateIsAtomicAndPublicReadsHideSecrets(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, func() time.Time { return now })
	secret := sealedFor(t, "task-1", `{"password":"do-not-leak"}`)
	created, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), secret, testSteps())
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != TaskQueued || len(mustSteps(t, repo, ctx, created.ID)) != 2 {
		t.Fatalf("created=%+v", created)
	}
	if _, err := repo.CreateTask(ctx, testTask("task-2", "server-1"), sealedFor(t, "task-2", `{}`), testSteps()); !errors.Is(err, ErrActiveTask) {
		t.Fatalf("active=%v", err)
	}
	got, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"task": got, "list": mustServerTasks(t, repo, ctx, "server-1"), "steps": mustSteps(t, repo, ctx, "task-1")} {
		data, _ := json.Marshal(value)
		text := string(data)
		if strings.Contains(text, "do-not-leak") || strings.Contains(text, base64.StdEncoding.EncodeToString(secret.Ciphertext)) || strings.Contains(text, "config_nonce") {
			t.Fatalf("%s leaked secret: %s", name, text)
		}
	}
}

func TestRepositoryClaimsOldestAndEnforcesTransitions(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-a")
	seedDeploymentServer(t, db, "server-b")
	now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	repo := NewRepository(db, func() time.Time { return now })
	if _, err := repo.CreateTask(ctx, testTask("later", "server-b"), sealedFor(t, "later", `{}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Minute)
	if _, err := repo.CreateTask(ctx, testTask("first", "server-a"), sealedFor(t, "first", `{}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := repo.ClaimNext(ctx, "worker", time.Minute)
	if err != nil || !ok || claimed.ID != "first" || claimed.Status != TaskRunning || claimed.StartedAt == nil {
		t.Fatalf("ClaimNext=(%+v,%v,%v)", claimed, ok, err)
	}
	if err := repo.StartStep(ctx, "first", "download", "Download", workerEvent("step-started")); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteStep(ctx, "first", "download", false, 0, workerEvent("step-completed")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("lower percent=%v", err)
	}
	if err := repo.CompleteStep(ctx, "first", "download", false, 50, workerEvent("step-completed")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RequestCancel(ctx, "first", EventInput{Type: "cancel-requested"}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetTask(ctx, "first")
	if got.Status != TaskRunning || !got.CancelRequested {
		t.Fatalf("cancel changed state=%+v", got)
	}
	if err := repo.Finish(ctx, "first", TaskCancelled, "", "", workerEvent("finished")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(ctx, "first", TaskCancelled, "", "", workerEvent("finished")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second finish=%v", err)
	}
	if err := repo.CompleteStep(ctx, "first", "install", false, 100, workerEvent("step-completed")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal complete=%v", err)
	}
}

func TestRepositoryRecoversRetriesAndResumesRegisteredValues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedDeploymentServer(t, db, "server-1")
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	repo := NewRepository(db, func() time.Time { return now })
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutValue(ctx, "task-1", "unknown", "x"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown key=%v", err)
	}
	if err := repo.PutValue(ctx, "task-1", "archive", "artifact.tgz"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	repo = NewRepository(db, func() time.Time { return now })
	if value, ok, err := repo.GetValue(ctx, "task-1", "archive"); err != nil || !ok || value != "artifact.tgz" {
		t.Fatalf("GetValue=(%q,%v,%v)", value, ok, err)
	}
	if err := repo.StartStep(ctx, "task-1", "download", "Download", workerEvent("step-started")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("start before claim=%v", err)
	}
	if _, ok, err := repo.ClaimNext(ctx, "worker", time.Minute); err != nil || !ok {
		t.Fatalf("claim=%v,%v", ok, err)
	}
	if err := repo.StartStep(ctx, "task-1", "download", "Download", workerEvent("step-started")); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteStep(ctx, "task-1", "download", false, 50, workerEvent("step-completed")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if n, err := repo.RecoverExpired(ctx); err != nil || n != 1 {
		t.Fatalf("recover=(%d,%v)", n, err)
	}
	steps := mustSteps(t, repo, ctx, "task-1")
	if steps[0].Status != StepSucceeded {
		t.Fatalf("completed step lost: %+v", steps[0])
	}
	if _, err := repo.Retry(ctx, "task-1", "retry", "actor", testSteps()); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry queued=%v", err)
	}
	if _, ok, err := repo.ClaimNext(ctx, "worker", time.Minute); err != nil || !ok {
		t.Fatalf("reclaim=%v,%v", ok, err)
	}
	if err := repo.Finish(ctx, "task-1", TaskFailed, "failed", "reason", workerEvent("finished")); err != nil {
		t.Fatal(err)
	}
	retry, err := repo.Retry(ctx, "task-1", "retry", "actor", nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != "retry" || retry.RetryOf != "task-1" || retry.Status != TaskQueued {
		t.Fatalf("retry=%+v", retry)
	}
	if value, ok, err := repo.GetValue(ctx, "task-1", "archive"); err != nil || !ok || value != "artifact.tgz" {
		t.Fatalf("value after reopen=(%q,%v,%v)", value, ok, err)
	}
}

func TestRepositoryOpenConfiguration(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, time.Now)
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{"ok":true}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	cipher, _ := NewSecretCipher(bytes32())
	got, err := repo.OpenTaskConfiguration(ctx, "task-1", cipher)
	if err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("open=(%s,%v)", got, err)
	}
}

func TestRepositoryFencesExpiredLeaseAndRestartsRunningStep(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	repo := NewRepository(db, func() time.Time { return now })
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ClaimNext(ctx, "old-worker", time.Minute); err != nil || !ok {
		t.Fatalf("claim old=(%v,%v)", ok, err)
	}
	if err := repo.StartStep(ctx, "task-1", "download", "Download", EventInput{Type: "started", Owner: "old-worker"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if n, err := repo.RecoverExpired(ctx); err != nil || n != 1 {
		t.Fatalf("recover=(%d,%v)", n, err)
	}
	steps := mustSteps(t, repo, ctx, "task-1")
	if steps[0].Status != StepPending || steps[0].StartedAt != nil || steps[0].FinishedAt != nil || steps[0].ErrorMessage != "" {
		t.Fatalf("recovered running step=%+v", steps[0])
	}
	if _, ok, err := repo.ClaimNext(ctx, "new-worker", time.Minute); err != nil || !ok {
		t.Fatalf("claim new=(%v,%v)", ok, err)
	}
	if err := repo.StartStep(ctx, "task-1", "download", "Download", EventInput{Type: "started", Owner: "old-worker"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old owner write=%v", err)
	}
	for name, err := range map[string]error{
		"complete": repo.CompleteStep(ctx, "task-1", "download", false, 50, EventInput{Type: "completed", Owner: "old-worker"}),
		"fail":     repo.FailStep(ctx, "task-1", "download", "no", EventInput{Type: "failed", Owner: "old-worker"}),
		"finish":   repo.Finish(ctx, "task-1", TaskFailed, "no", "no", EventInput{Type: "finished", Owner: "old-worker"}),
	} {
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("old owner %s=%v", name, err)
		}
	}
	if err := repo.StartStep(ctx, "task-1", "download", "Download", EventInput{Type: "started", Owner: "new-worker"}); err != nil {
		t.Fatalf("new owner restart=%v", err)
	}
}

func TestRepositoryRequiresSequentialStepsAndCompletePlanForSuccess(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	now := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
	repo := NewRepository(db, func() time.Time { return now })
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), testSteps()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ClaimNext(ctx, "worker", time.Minute); err != nil || !ok {
		t.Fatalf("claim=(%v,%v)", ok, err)
	}
	if err := repo.StartStep(ctx, "task-1", "install", "Install", workerEvent("started")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("out of order=%v", err)
	}
	if err := repo.Finish(ctx, "task-1", TaskSucceeded, "", "", workerEvent("finished")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("early success=%v", err)
	}
}

func openDeploymentDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "deployment.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func seedDeploymentServer(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, created_at, updated_at) VALUES (?, 'password', x'01', x'02', 'now', 'now')`, "cred-"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_servers (id, name, address, ssh_port, username, credential_id, status, created_at, updated_at) VALUES (?, ?, '192.0.2.1', 22, 'root', ?, 'online', 'now', 'now')`, id, id, "cred-"+id); err != nil {
		t.Fatal(err)
	}
}
func testTask(id, serverID string) Task {
	return Task{ID: id, ServerID: serverID, ProjectID: "project", Version: "1", Action: TaskActionInstall, Actor: "actor"}
}
func testSteps() []StepDefinition {
	return []StepDefinition{{ID: "download", Label: "Download", Percent: 50, ValueKeys: []string{"archive"}}, {ID: "install", Label: "Install", Percent: 100}}
}
func sealedFor(t *testing.T, id, value string) SealedSecret {
	t.Helper()
	cipher, err := NewSecretCipher(bytes32())
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(TaskConfigScope, id, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
func bytes32() []byte                    { return []byte(strings.Repeat("k", 32)) }
func workerEvent(kind string) EventInput { return EventInput{Type: kind, Owner: "worker"} }
func mustSteps(t *testing.T, r *Repository, ctx context.Context, id string) []Step {
	t.Helper()
	value, err := r.ListSteps(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func mustServerTasks(t *testing.T, r *Repository, ctx context.Context, id string) []Task {
	t.Helper()
	value, err := r.ListServerTasks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type testInstaller struct{ project Project }

func (i testInstaller) Project() Project { return i.project }
func (i testInstaller) NormalizeConfiguration(value json.RawMessage) (json.RawMessage, error) {
	return value, nil
}
func (i testInstaller) BuildPlan(Task, assets.Server, json.RawMessage) ([]StepDefinition, error) {
	return nil, nil
}
