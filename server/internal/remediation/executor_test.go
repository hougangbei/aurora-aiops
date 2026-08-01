package remediation

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/audit"
	"github.com/heihuzicity-tech/kubejojo/server/internal/policy"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

type recordingClient struct {
	events  *[]string
	err     error
	waitErr error
}

func (c *recordingClient) RestartDeployment(_ context.Context, _, _ string) error {
	*c.events = append(*c.events, "write:restart")
	return c.err
}
func (c *recordingClient) ScaleDeployment(_ context.Context, _, _ string, _ int32) error {
	*c.events = append(*c.events, "write:scale")
	return c.err
}
func (c *recordingClient) SuspendCronJob(_ context.Context, _, _ string) error {
	*c.events = append(*c.events, "write:suspend")
	return c.err
}
func (c *recordingClient) WaitDeploymentAvailable(_ context.Context, _, _ string) error {
	*c.events = append(*c.events, "observe")
	return c.waitErr
}

type recordingSnapshotter struct {
	events *[]string
	err    error
}

func (s *recordingSnapshotter) Snapshot(_ context.Context, incidentID, ns, kind, name string) (Snapshot, error) {
	*s.events = append(*s.events, "snapshot")
	return Snapshot{ID: "snap-1", IncidentID: incidentID, Kind: kind, Namespace: ns, Name: name}, s.err
}

type recordingStore struct {
	events      *[]string
	hasExecuted bool
	saveErr     error
}

func (s *recordingStore) Save(_ context.Context, _ Snapshot) error {
	*s.events = append(*s.events, "save-snapshot")
	return s.saveErr
}
func (s *recordingStore) Get(_ context.Context, _ string) (Snapshot, error) {
	return Snapshot{}, ErrSnapshotNotFound
}
func (s *recordingStore) ListByIncident(_ context.Context, _ string) ([]Snapshot, error) {
	return nil, nil
}
func (s *recordingStore) HasExecuted(_ context.Context, _, _, _ string) (bool, error) {
	*s.events = append(*s.events, "check-idempotency")
	return s.hasExecuted, nil
}
func (s *recordingStore) DeleteByIncident(_ context.Context, _ string) error { return nil }

type recordingAudit struct {
	repo   audit.Repository
	events *[]string
	err    error
}

func (a *recordingAudit) Append(ctx context.Context, record audit.Record) (audit.Record, error) {
	*a.events = append(*a.events, "audit:"+record.Action)
	if a.err != nil {
		return audit.Record{}, a.err
	}
	return a.repo.Append(ctx, record)
}
func (a *recordingAudit) List(ctx context.Context) ([]audit.Record, error) { return a.repo.List(ctx) }

func newExecutorHarness(t *testing.T) (*Executor, *[]string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	events := &[]string{}
	client := &recordingClient{events: events}
	snapshotter := &recordingSnapshotter{events: events}
	snapshots := &recordingStore{events: events}
	auditRepo := &recordingAudit{repo: audit.NewRepository(db), events: events}
	return NewExecutor(client, snapshotter, snapshots, auditRepo), events
}

func approvedIncident() aiops.Incident {
	return aiops.Incident{ID: "inc-1", Namespace: "default", Status: aiops.StatusApproved}
}

func restartAction() policy.Action {
	return policy.Action{Kind: policy.ActionRestartDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api"}
}

func TestExecuteOrder(t *testing.T) {
	executor, events := newExecutorHarness(t)

	if err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{restartAction()}, "admin"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"check-idempotency",
		"snapshot",
		"save-snapshot",
		"audit:execution_started",
		"write:restart",
		"observe",
		"audit:execution_succeeded",
	}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("execution order:\n got %v\nwant %v", *events, want)
	}
}

func TestExecuteRejectsNonApproved(t *testing.T) {
	executor, events := newExecutorHarness(t)
	incident := approvedIncident()
	incident.Status = aiops.StatusAwaitingApproval

	err := executor.Execute(context.Background(), incident, []policy.Action{restartAction()}, "admin")
	if !errors.Is(err, ErrNotApproved) {
		t.Fatalf("err=%v want ErrNotApproved", err)
	}
	if len(*events) != 0 {
		t.Fatalf("unexpected events before approval: %v", *events)
	}
}

func TestExecuteRejectsPolicyDeniedAction(t *testing.T) {
	executor, events := newExecutorHarness(t)
	action := policy.Action{Kind: "shell", Namespace: "default", ResourceKind: "Pod", ResourceName: "api", Parameters: map[string]string{"command": "rm -rf /"}}

	err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{action}, "admin")
	if !errors.Is(err, policy.ErrActionDenied) {
		t.Fatalf("err=%v want policy.ErrActionDenied", err)
	}
	for _, event := range *events {
		if len(event) >= 6 && event[:6] == "write:" {
			t.Fatalf("write happened for denied action: %v", *events)
		}
	}
}

func TestExecuteBlocksDuplicateWrite(t *testing.T) {
	executor, _ := newExecutorHarness(t)
	// The store reports an action already executed for this incident.
	executor.snapshots.(*recordingStore).hasExecuted = true

	err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{restartAction()}, "admin")
	if !errors.Is(err, ErrAlreadyExecuted) {
		t.Fatalf("err=%v want ErrAlreadyExecuted", err)
	}
}

func TestExecuteSnapshotFailureBlocksWrite(t *testing.T) {
	executor, events := newExecutorHarness(t)
	executor.snapshotter.(*recordingSnapshotter).err = errors.New("snapshot boom")

	err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{restartAction()}, "admin")
	if err == nil {
		t.Fatal("expected error on snapshot failure")
	}
	for _, event := range *events {
		if len(event) >= 6 && event[:6] == "write:" {
			t.Fatalf("write happened despite snapshot failure: %v", *events)
		}
	}
}

func TestExecuteRolloutTimeoutKeepsSnapshot(t *testing.T) {
	executor, events := newExecutorHarness(t)
	executor.client.(*recordingClient).waitErr = errors.New("rollout timeout")

	err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{restartAction()}, "admin")
	if err == nil {
		t.Fatal("expected rollout timeout error")
	}
	sawSnapshot := false
	sawFailedAudit := false
	for _, event := range *events {
		if event == "save-snapshot" {
			sawSnapshot = true
		}
		if event == "audit:execution_failed" {
			sawFailedAudit = true
		}
	}
	if !sawSnapshot {
		t.Fatalf("snapshot not retained on rollout timeout: %v", *events)
	}
	if !sawFailedAudit {
		t.Fatalf("failure not audited: %v", *events)
	}
}

func TestExecuteAuditStartFailureBlocksWrite(t *testing.T) {
	executor, events := newExecutorHarness(t)
	executor.audit.(*recordingAudit).err = errors.New("audit down")

	err := executor.Execute(context.Background(), approvedIncident(), []policy.Action{restartAction()}, "admin")
	if err == nil {
		t.Fatal("expected error when audit append fails")
	}
	for _, event := range *events {
		if len(event) >= 6 && event[:6] == "write:" {
			t.Fatalf("write happened despite audit failure: %v", *events)
		}
	}
}
