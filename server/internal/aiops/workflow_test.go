package aiops

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/evidence"
	"github.com/heihuzicity-tech/kubejojo/server/internal/llm"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

func newWorkflowTest(t *testing.T) (*sql.DB, *Service, IncidentRepository, RunRepository, evidence.Repository) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	incRepo := NewRepository(db)
	return db, NewService(incRepo), incRepo, NewRunRepository(db), evidence.NewRepository(db)
}

type fakeCollector struct {
	nodes []evidence.Node
	edges []evidence.Edge
	err   error
	calls int
}

func (f *fakeCollector) Collect(_ context.Context, _ evidence.Target, _ evidence.Window) ([]evidence.Node, []evidence.Edge, error) {
	f.calls++
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.nodes, f.edges, nil
}

func newFakeCollector() *fakeCollector {
	nodes, edges := makeFakeEvidence()
	return &fakeCollector{nodes: nodes, edges: edges}
}

func makeFakeEvidence() ([]evidence.Node, []evidence.Edge) {
	t0 := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	nodes := []evidence.Node{
		{ID: "snapshot", Kind: evidence.NodeKindSnapshot, Payload: `{"phase":"Running"}`, ObservedAt: t0},
		{ID: "log-app", Kind: evidence.NodeKindLog, Payload: "level=error boom", ObservedAt: t0.Add(time.Second)},
	}
	edges := []evidence.Edge{{FromID: "log-app", ToID: "snapshot", Relation: evidence.RelationSupports}}
	return nodes, edges
}

type fakeLLM struct {
	mu    sync.Mutex
	calls int
	err   error
	enter func()
	texts map[string]string
}

func (f *fakeLLM) GenerateJSON(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.enter != nil {
		f.enter()
	}
	if f.err != nil {
		return llm.Response{}, f.err
	}
	content := ""
	if len(req.Messages) > 0 {
		content = req.Messages[0].Content
	}
	if text, ok := f.textFor(content); ok {
		return llm.Response{Text: text, Model: "fake-model"}, nil
	}
	switch {
	case strings.Contains(content, "TRIAGE role"):
		return llm.Response{Text: `{"summary":"pod crash loop","severity":"critical","rationale":"restarts"}`, Model: "fake-model"}, nil
	case strings.Contains(content, "COLLECTOR role"):
		return llm.Response{Text: `{"targetKind":"Pod","targetName":"api-0","commands":["collect snapshot"]}`, Model: "fake-model"}, nil
	case strings.Contains(content, "ROOT CAUSE role"):
		return llm.Response{Text: `{"candidates":[{"summary":"oomkilled","confidence":0.9,"evidenceIds":["snapshot"],"verificationSteps":["check events"]}]}`, Model: "fake-model"}, nil
	case strings.Contains(content, "REMEDIATION role"):
		return llm.Response{Text: `{"actions":[{"command":"kubectl rollout restart deployment/api","reason":"restart after crash loop","risk":"low"}]}`, Model: "fake-model"}, nil
	case strings.Contains(content, "RISK REVIEW role"):
		return llm.Response{Text: `{"riskLevel":"low","approved":true}`, Model: "fake-model"}, nil
	}
	return llm.Response{}, errors.New("unrecognized role prompt")
}

func (f *fakeLLM) textFor(content string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for marker, text := range f.texts {
		if strings.Contains(content, marker) {
			return text, true
		}
	}
	return "", false
}

func buildWorkflow(db *sql.DB, incRepo IncidentRepository, runRepo RunRepository,
	evRepo evidence.Repository, collector evidence.Collector, llm LLMClient, modelConfigured bool) *Workflow {
	return NewWorkflow(WorkflowOptions{
		DB:              db,
		Incidents:       incRepo,
		Runs:            runRepo,
		Evidence:        evRepo,
		Collector:       collector,
		LLM:             llm,
		ModelConfigured: modelConfigured,
		Model:           "fake-model",
	})
}

func createWorkflowIncident(t *testing.T, svc *Service) Incident {
	t.Helper()
	inc, err := svc.Create(context.Background(), CreateIncidentInput{
		Summary:      "pod crash loop",
		Severity:     "critical",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
	})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}
	return inc
}

func TestWorkflowRunsAllRoles(t *testing.T) {
	db, svc, incRepo, runRepo, evRepo := newWorkflowTest(t)
	ctx := context.Background()
	inc := createWorkflowIncident(t, svc)
	collector := newFakeCollector()
	llm := &fakeLLM{}
	wf := buildWorkflow(db, incRepo, runRepo, evRepo, collector, llm, true)

	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}

	runs, err := runRepo.ListRuns(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 5 {
		t.Fatalf("runs=%d want 5", len(runs))
	}
	wantRoles := []string{"triage", "collector", "root_cause", "remediation", "risk_review"}
	for i, r := range runs {
		if r.Role != wantRoles[i] {
			t.Fatalf("run[%d].role=%s want %s", i, r.Role, wantRoles[i])
		}
		if r.Status != RunStatusSucceeded {
			t.Fatalf("run[%d] %s status=%s", i, r.Role, r.Status)
		}
		if r.Attempt != 1 {
			t.Fatalf("run[%d] attempt=%d want 1", i, r.Attempt)
		}
		if r.Output == "" {
			t.Fatalf("run[%d] %s empty output", i, r.Role)
		}
	}

	nodes, err := evRepo.ListNodes(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("evidence nodes=%d want 2", len(nodes))
	}
	for _, n := range nodes {
		if n.IncidentID != inc.ID {
			t.Fatalf("node %s incident=%s", n.ID, n.IncidentID)
		}
	}
	if collector.calls != 1 {
		t.Fatalf("collector calls=%d want 1", collector.calls)
	}
}

func TestWorkflowResumesWithoutReCollecting(t *testing.T) {
	db, svc, incRepo, runRepo, evRepo := newWorkflowTest(t)
	ctx := context.Background()
	inc := createWorkflowIncident(t, svc)
	collector := newFakeCollector()

	// Phase 1: no model. Deterministic triage + collector run; root_cause is
	// recorded as skipped/model_unavailable and the workflow stops at collecting.
	wf1 := buildWorkflow(db, incRepo, runRepo, evRepo, collector, nil, false)
	if err := wf1.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	got1, err := svc.Get(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Status != StatusCollecting {
		t.Fatalf("phase1 status=%s want collecting", got1.Status)
	}
	runs1, _ := runRepo.ListRuns(ctx, inc.ID)
	if len(runs1) != 3 {
		t.Fatalf("phase1 runs=%d want 3", len(runs1))
	}
	if runs1[0].Role != "triage" || runs1[0].Status != RunStatusSucceeded {
		t.Fatalf("phase1 run[0]=%+v", runs1[0])
	}
	if runs1[1].Role != "collector" || runs1[1].Status != RunStatusSucceeded {
		t.Fatalf("phase1 run[1]=%+v", runs1[1])
	}
	if runs1[2].Role != "root_cause" || runs1[2].Status != RunStatusSkipped || runs1[2].Summary != ModelUnavailable {
		t.Fatalf("phase1 run[2]=%+v", runs1[2])
	}
	if collector.calls != 1 {
		t.Fatalf("phase1 collector calls=%d want 1", collector.calls)
	}

	// Phase 2: restart with a model. The completed collector step must not be
	// re-executed; the workflow resumes at root_cause.
	llm := &fakeLLM{}
	wf2 := buildWorkflow(db, incRepo, runRepo, evRepo, collector, llm, true)
	if err := wf2.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	got2, _ := svc.Get(ctx, inc.ID)
	if got2.Status != StatusAwaitingApproval {
		t.Fatalf("phase2 status=%s want awaiting_approval", got2.Status)
	}

	runs2, _ := runRepo.ListRuns(ctx, inc.ID)
	collectorSucceeded := 0
	rootCauseSucceeded := false
	for _, r := range runs2 {
		if r.Role == "collector" && r.Status == RunStatusSucceeded {
			collectorSucceeded++
		}
		if r.Role == "root_cause" && r.Status == RunStatusSucceeded {
			rootCauseSucceeded = true
		}
	}
	if collectorSucceeded != 1 {
		t.Fatalf("collector succeeded runs=%d want 1 (recollection)", collectorSucceeded)
	}
	if collector.calls != 1 {
		t.Fatalf("collector collect calls=%d want 1", collector.calls)
	}
	if !rootCauseSucceeded {
		t.Fatal("root_cause did not succeed after restart")
	}
}

func TestWorkflowConcurrentTriggerSerializedByLock(t *testing.T) {
	db, svc, incRepo, runRepo, evRepo := newWorkflowTest(t)
	ctx := context.Background()
	inc := createWorkflowIncident(t, svc)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	llm := &fakeLLM{enter: func() {
		enterOnce.Do(func() { close(entered) })
		<-release
	}}
	collector := newFakeCollector()
	wf := buildWorkflow(db, incRepo, runRepo, evRepo, collector, llm, true)

	var errA, errB error
	done := make(chan struct{})
	go func() {
		defer close(done)
		errA = wf.Run(ctx, inc.ID)
	}()
	<-entered // runner A holds the workflow lock inside the first llm call

	errB = wf.Run(ctx, inc.ID) // runner B must lose the lock
	close(release)
	<-done

	if errB == nil || !errors.Is(errB, ErrWorkflowLocked) {
		t.Fatalf("errB=%v want ErrWorkflowLocked", errB)
	}
	if errA != nil {
		t.Fatalf("errA=%v", errA)
	}
}

func TestWorkflowInvalidModelOutputFailsIncident(t *testing.T) {
	db, svc, incRepo, runRepo, evRepo := newWorkflowTest(t)
	ctx := context.Background()
	inc := createWorkflowIncident(t, svc)
	llm := &fakeLLM{texts: map[string]string{"TRIAGE role": "definitely not json"}}
	collector := newFakeCollector()
	wf := buildWorkflow(db, incRepo, runRepo, evRepo, collector, llm, true)

	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, inc.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status=%s want failed", got.Status)
	}
	runs, _ := runRepo.ListRuns(ctx, inc.ID)
	if len(runs) != 1 || runs[0].Role != "triage" || runs[0].Status != RunStatusFailed {
		t.Fatalf("runs=%+v", runs)
	}
	if runs[0].Error == "" {
		t.Fatal("failed run has no error recorded")
	}
	// No action executed: collector and later roles never ran.
	if collector.calls != 0 {
		t.Fatalf("collector ran after invalid output: calls=%d", collector.calls)
	}
}

func TestWorkflowCollectorHardFailureFailsIncident(t *testing.T) {
	db, svc, incRepo, runRepo, evRepo := newWorkflowTest(t)
	ctx := context.Background()
	inc := createWorkflowIncident(t, svc)
	collector := &fakeCollector{err: errors.New("core api unreachable")}
	llm := &fakeLLM{}
	wf := buildWorkflow(db, incRepo, runRepo, evRepo, collector, llm, true)

	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, inc.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status=%s want failed", got.Status)
	}
	runs, _ := runRepo.ListRuns(ctx, inc.ID)
	if len(runs) != 2 {
		t.Fatalf("runs=%d want 2", len(runs))
	}
	if runs[0].Role != "triage" || runs[0].Status != RunStatusSucceeded {
		t.Fatalf("run[0]=%+v", runs[0])
	}
	if runs[1].Role != "collector" || runs[1].Status != RunStatusFailed {
		t.Fatalf("run[1]=%+v", runs[1])
	}
	nodes, _ := evRepo.ListNodes(ctx, inc.ID)
	if len(nodes) != 0 {
		t.Fatalf("evidence persisted on hard failure: %d", len(nodes))
	}
}
