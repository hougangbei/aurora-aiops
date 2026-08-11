package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
)

func TestWorkerRunsPlanAndCompletesTask(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, time.Now)
	cipher, _ := NewSecretCipher(bytes32())
	inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}}
	inst.steps = []StepDefinition{
		{ID: "one", Label: "One", Percent: 50, Probe: func(context.Context, ExecutionContext) (bool, error) { return false, nil }, Run: func(ctx context.Context, e ExecutionContext) error { _, err := e.Run(ctx, "one", 1); return err }},
		{ID: "two", Label: "Two", Percent: 100, Run: func(ctx context.Context, e ExecutionContext) error { _, err := e.Run(ctx, "two", 1); return err }},
	}
	var catalog Catalog
	if err := catalog.Register(inst); err != nil {
		t.Fatal(err)
	}
	provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{"secret":"swordfish"}`), inst.steps); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: time.Minute, Poll: time.Millisecond})
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskSucceeded || task.Percent != 100 {
		t.Fatalf("task=%+v", task)
	}
	if len(provider.ctx.runs) != 2 {
		t.Fatalf("runs=%v", provider.ctx.runs)
	}
}

func TestWorkerProbeSkipsRunAndKeepsMonotonicPercent(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, time.Now)
	cipher, _ := NewSecretCipher(bytes32())
	called := false
	inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{
		{ID: "one", Label: "One", Percent: 50, Probe: func(context.Context, ExecutionContext) (bool, error) { return true, nil }, Run: func(context.Context, ExecutionContext) error { called = true; return nil }},
		{ID: "two", Label: "Two", Percent: 100, Run: func(context.Context, ExecutionContext) error { return nil }},
	}}
	var catalog Catalog
	_ = catalog.Register(inst)
	provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), inst.steps); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: time.Minute})
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("probe-success step ran unexpectedly")
	}
	steps, _ := repo.ListSteps(ctx, "task-1")
	if len(steps) != 2 || steps[0].Status != StepSkipped || steps[1].Status != StepSucceeded {
		t.Fatalf("steps=%+v", steps)
	}
}

func TestWorkerRunFailureAndPanicAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		run      func(context.Context, ExecutionContext) error
		wantCode string
	}{
		{name: "error", run: func(context.Context, ExecutionContext) error { return errors.New("password=swordfish") }, wantCode: "STEP_FAILED"},
		{name: "panic", run: func(context.Context, ExecutionContext) error { panic("token=swordfish") }, wantCode: "INSTALLER_PANIC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := context.Background(), openDeploymentDB(t)
			seedDeploymentServer(t, db, "server-1")
			repo := NewRepository(db, time.Now)
			cipher, _ := NewSecretCipher(bytes32())
			inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{{ID: "one", Label: "One", Percent: 100, Run: tc.run}}}
			var catalog Catalog
			_ = catalog.Register(inst)
			provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
			if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{"secret":"swordfish"}`), inst.steps); err != nil {
				t.Fatal(err)
			}
			w := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: time.Minute})
			if _, err := w.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			task, _ := repo.GetTask(ctx, "task-1")
			if task.Status != TaskFailed || task.ErrorCode != tc.wantCode {
				t.Fatalf("task=%+v", task)
			}
			if strings.Contains(task.ErrorMessage, "swordfish") {
				t.Fatalf("secret leaked: %+v", task)
			}
			rows, err := db.Query(`SELECT payload FROM deployment_events WHERE task_id='task-1'`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var payload string
				if err := rows.Scan(&payload); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(payload, "swordfish") {
					t.Fatalf("secret leaked in event: %s", payload)
				}
			}
		})
	}
}

func TestWorkerCancelsBeforeNextStep(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, time.Now)
	cipher, _ := NewSecretCipher(bytes32())
	inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{
		{ID: "one", Label: "One", Percent: 50, Run: func(context.Context, ExecutionContext) error { return nil }},
		{ID: "two", Label: "Two", Percent: 100, Run: func(context.Context, ExecutionContext) error { t.Fatal("cancelled task ran second step"); return nil }},
	}}
	var catalog Catalog
	_ = catalog.Register(inst)
	provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), inst.steps); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ClaimNext(ctx, "worker-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.RequestCancel(ctx, "task-1", EventInput{Type: "cancel-requested"}); err != nil {
		t.Fatal(err)
	}
	// RunOnce sees no queued task; execute the already claimed task directly via the worker helper.
	w := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: time.Minute})
	if err := w.executeClaimed(ctx, mustTask(t, repo, ctx, "task-1")); err != nil {
		t.Fatal(err)
	}
	task, _ := repo.GetTask(ctx, "task-1")
	if task.Status != TaskCancelled || task.Percent != 0 {
		t.Fatalf("task=%+v", task)
	}
}

func TestWorkerTimeoutAndLeaseRenewal(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		ctx, db := context.Background(), openDeploymentDB(t)
		seedDeploymentServer(t, db, "server-1")
		repo := NewRepository(db, time.Now)
		cipher, _ := NewSecretCipher(bytes32())
		inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{{ID: "one", Label: "One", Percent: 100, Timeout: 10 * time.Millisecond, Run: func(ctx context.Context, _ ExecutionContext) error { <-ctx.Done(); return ctx.Err() }}}}
		var catalog Catalog
		_ = catalog.Register(inst)
		provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
		if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), inst.steps); err != nil {
			t.Fatal(err)
		}
		if _, err := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: time.Minute}).RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		task, _ := repo.GetTask(ctx, "task-1")
		if task.Status != TaskFailed || task.ErrorCode != "STEP_TIMEOUT" {
			t.Fatalf("task=%+v", task)
		}
	})
	t.Run("renewal", func(t *testing.T) {
		ctx, db := context.Background(), openDeploymentDB(t)
		seedDeploymentServer(t, db, "server-1")
		repo := NewRepository(db, time.Now)
		cipher, _ := NewSecretCipher(bytes32())
		inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{{ID: "one", Label: "One", Percent: 100, Run: func(ctx context.Context, _ ExecutionContext) error { time.Sleep(70 * time.Millisecond); return nil }}}}
		var catalog Catalog
		_ = catalog.Register(inst)
		provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
		if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), inst.steps); err != nil {
			t.Fatal(err)
		}
		if _, err := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: 30 * time.Millisecond}).RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		task, _ := repo.GetTask(ctx, "task-1")
		if task.Status != TaskSucceeded {
			t.Fatalf("task=%+v", task)
		}
	})
}

func TestWorkerCancelsDuringCommand(t *testing.T) {
	ctx, db := context.Background(), openDeploymentDB(t)
	seedDeploymentServer(t, db, "server-1")
	repo := NewRepository(db, time.Now)
	cipher, _ := NewSecretCipher(bytes32())
	started := make(chan struct{})
	inst := &workerTestInstaller{project: Project{ID: "project", Versions: []string{"1"}, SupportedArchitectures: []string{"amd64"}}, steps: []StepDefinition{{ID: "one", Label: "One", Percent: 100, Run: func(ctx context.Context, _ ExecutionContext) error { close(started); <-ctx.Done(); return ctx.Err() }}}}
	var catalog Catalog
	_ = catalog.Register(inst)
	provider := &workerTestProvider{server: assets.Server{ID: "server-1", Architecture: "amd64"}}
	if _, err := repo.CreateTask(ctx, testTask("task-1", "server-1"), sealedFor(t, "task-1", `{}`), inst.steps); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(repo, &catalog, cipher, provider, WorkerOptions{Owner: "worker-1", Lease: 30 * time.Millisecond})
	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("step did not start")
	}
	if err := repo.RequestCancel(ctx, "task-1", EventInput{Type: "cancel-requested"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel")
	}
	task, _ := repo.GetTask(ctx, "task-1")
	if task.Status != TaskCancelled {
		t.Fatalf("task=%+v", task)
	}
}

func mustTask(t *testing.T, r *Repository, ctx context.Context, id string) Task {
	t.Helper()
	v, err := r.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type workerTestInstaller struct {
	project Project
	steps   []StepDefinition
}

func (i *workerTestInstaller) Project() Project { return i.project }
func (i *workerTestInstaller) NormalizeConfiguration(v json.RawMessage) (json.RawMessage, error) {
	return v, nil
}
func (i *workerTestInstaller) BuildPlan(Task, assets.Server, json.RawMessage) ([]StepDefinition, error) {
	return i.steps, nil
}

type workerTestContext struct {
	mu     sync.Mutex
	runs   []string
	logs   []string
	values map[string]string
}

func (c *workerTestContext) Server() assets.Server {
	return assets.Server{ID: "server-1", Architecture: "amd64"}
}
func (c *workerTestContext) Run(_ context.Context, command string, _ int64) (assets.CommandResult, error) {
	c.mu.Lock()
	c.runs = append(c.runs, command)
	c.mu.Unlock()
	return assets.CommandResult{ExitCode: 0}, nil
}
func (c *workerTestContext) Upload(context.Context, io.Reader, int64, string, fs.FileMode) error {
	return nil
}
func (c *workerTestContext) Log(v string) { c.mu.Lock(); c.logs = append(c.logs, v); c.mu.Unlock() }
func (c *workerTestContext) SetValue(k, v string) error {
	if c.values == nil {
		c.values = map[string]string{}
	}
	c.values[k] = v
	return nil
}
func (c *workerTestContext) Value(k string) (string, bool) { v, ok := c.values[k]; return v, ok }

type workerTestProvider struct {
	server assets.Server
	ctx    *workerTestContext
}

func (p *workerTestProvider) ExecutionContext(context.Context, string, string) (assets.Server, ExecutionContext, error) {
	if p.ctx == nil {
		p.ctx = &workerTestContext{}
	}
	return p.server, p.ctx, nil
}
