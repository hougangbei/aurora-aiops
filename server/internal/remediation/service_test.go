package remediation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/audit"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// newRemediationTestService wires a remediation Service against a real SQLite
// store. Approve/Reject exercise the aiops CAS, the latest-remediation loader
// and the audit chain, so the executor, kube client and snapshot store are nil.
func newRemediationTestService(t *testing.T) (*Service, *aiops.Service, aiops.RunRepository, audit.Repository) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "remediation.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	aiopsSvc := aiops.NewService(aiops.NewRepository(db))
	auditRepo := audit.NewRepository(db)
	runsRepo := aiops.NewRunRepository(db)
	svc := NewService(aiopsSvc, runsRepo, nil, nil, auditRepo, nil)
	return svc, aiopsSvc, runsRepo, auditRepo
}

func awaitApprovalIncident(t *testing.T, aiopsSvc *aiops.Service) aiops.Incident {
	t.Helper()
	ctx := context.Background()
	inc, err := aiopsSvc.Create(ctx, aiops.CreateIncidentInput{
		Summary:      "race decision",
		Severity:     "critical",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, to := range []aiops.Status{
		aiops.StatusTriaging, aiops.StatusCollecting, aiops.StatusAnalyzing,
		aiops.StatusProposing, aiops.StatusAwaitingApproval,
	} {
		if err := aiopsSvc.Advance(ctx, inc.ID, to); err != nil {
			t.Fatalf("advance to %s: %v", to, err)
		}
	}
	return inc
}

func storeRemediationRunAt(t *testing.T, runs aiops.RunRepository, incidentID string, attempt int, actions ...aiops.RemediationAction) {
	t.Helper()
	raw, err := json.Marshal(aiops.RemediationOutput{Actions: actions})
	if err != nil {
		t.Fatalf("marshal remediation: %v", err)
	}
	run := aiops.AgentRun{
		ID:          fmt.Sprintf("%s-remediation-%d", incidentID, attempt),
		IncidentID:  incidentID,
		Role:        "remediation",
		Attempt:     attempt,
		Status:      aiops.RunStatusSucceeded,
		Output:      string(raw),
		StartedAt:   time.Now().UTC(),
		CompletedAt: time.Now().UTC(),
	}
	if err := runs.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create remediation run: %v", err)
	}
}

func storeRemediationRun(t *testing.T, runs aiops.RunRepository, incidentID string, actions ...aiops.RemediationAction) {
	storeRemediationRunAt(t, runs, incidentID, 1, actions...)
}

func countAudit(records []audit.Record, action string) int {
	n := 0
	for _, r := range records {
		if r.Action == action {
			n++
		}
	}
	return n
}

// TestApproveRejectConcurrentDecisionSingleWinner proves the compare-and-swap
// decision path has exactly one winner under concurrent approve/reject: one call
// returns nil, the other ErrStateTransitionConflict, only one audit record is
// appended, and the stored status is a single terminal decision.
func TestApproveRejectConcurrentDecisionSingleWinner(t *testing.T) {
	svc, aiopsSvc, runsRepo, auditRepo := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)
	storeRemediationRun(t, runsRepo, inc.ID, aiops.RemediationAction{
		Command: "suspend cronjob", Reason: "r", Risk: "low",
		Kind: "suspend_cronjob", Namespace: inc.Namespace,
		ResourceKind: "CronJob", ResourceName: "job-0",
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	var approveErr, rejectErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		approveErr = svc.Approve(ctx, inc.ID, "operator-a", "approved: plan is safe")
	}()
	go func() {
		defer wg.Done()
		<-start
		rejectErr = svc.Reject(ctx, inc.ID, "operator-b", "rejected: too risky")
	}()
	close(start)
	wg.Wait()

	nilCount, conflictCount := 0, 0
	for _, err := range []error{approveErr, rejectErr} {
		if err == nil {
			nilCount++
		}
		if errors.Is(err, aiops.ErrStateTransitionConflict) {
			conflictCount++
		}
	}
	if nilCount != 1 {
		t.Fatalf("nilCount=%d want 1 (approve=%v reject=%v)", nilCount, approveErr, rejectErr)
	}
	if conflictCount != 1 {
		t.Fatalf("conflictCount=%d want 1 (approve=%v reject=%v)", conflictCount, approveErr, rejectErr)
	}

	records, err := auditRepo.List(ctx)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if n := countAudit(records, "approve-remediation") + countAudit(records, "reject-remediation"); n != 1 {
		t.Fatalf("decision audit records=%d want exactly 1 (loser must not audit)", n)
	}

	got, err := aiopsSvc.Get(ctx, inc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != aiops.StatusApproved && got.Status != aiops.StatusRejected {
		t.Fatalf("stored status=%s want a single terminal decision", got.Status)
	}
}

// TestApproveAllowedStructuredPlanAdvancesAndAudits verifies a policy-permitted
// structured plan advances to approved and appends exactly one audit record.
func TestApproveAllowedStructuredPlanAdvancesAndAudits(t *testing.T) {
	svc, aiopsSvc, runsRepo, auditRepo := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)
	storeRemediationRun(t, runsRepo, inc.ID, aiops.RemediationAction{
		Command: "suspend cronjob", Reason: "stop noisy job", Risk: "low",
		Kind: "suspend_cronjob", Namespace: inc.Namespace,
		ResourceKind: "CronJob", ResourceName: "job-0",
	})

	if err := svc.Approve(ctx, inc.ID, "operator", "plan is safe"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _ := aiopsSvc.Get(ctx, inc.ID)
	if got.Status != aiops.StatusApproved {
		t.Fatalf("status=%s want approved", got.Status)
	}
	records, _ := auditRepo.List(ctx)
	if n := countAudit(records, "approve-remediation"); n != 1 {
		t.Fatalf("approve audit records=%d want 1", n)
	}
}

// TestApproveDisplayOnlyPlanRejected verifies a plan with only display-only
// actions (no structured Kind) is fail-closed: ErrActionNotAllowed, the incident
// stays awaiting_approval, and no audit record is appended.
func TestApproveDisplayOnlyPlanRejected(t *testing.T) {
	svc, aiopsSvc, runsRepo, auditRepo := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)
	storeRemediationRun(t, runsRepo, inc.ID, aiops.RemediationAction{
		Command: "investigate manually", Reason: "no structured action", Risk: "low",
	})

	err := svc.Approve(ctx, inc.ID, "operator", "plan is safe")
	if !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("err=%v want ErrActionNotAllowed", err)
	}
	got, _ := aiopsSvc.Get(ctx, inc.ID)
	if got.Status != aiops.StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}
	records, _ := auditRepo.List(ctx)
	if n := countAudit(records, "approve-remediation"); n != 0 {
		t.Fatalf("denied approve must not audit, got %d", n)
	}
}

// TestApproveNamespaceMismatchRejected verifies a plan whose action targets a
// different namespace than the incident is fail-closed.
func TestApproveNamespaceMismatchRejected(t *testing.T) {
	svc, aiopsSvc, runsRepo, _ := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)
	storeRemediationRun(t, runsRepo, inc.ID, aiops.RemediationAction{
		Command: "restart deployment", Reason: "r", Risk: "low",
		Kind: "restart_deployment", Namespace: "other",
		ResourceKind: "Deployment", ResourceName: "api-0",
	})

	if err := svc.Approve(ctx, inc.ID, "operator", "plan is safe"); !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("err=%v want ErrActionNotAllowed", err)
	}
	got, _ := aiopsSvc.Get(ctx, inc.ID)
	if got.Status != aiops.StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}
}

// TestApproveRechecksPolicyOnStoredPlan verifies the approval gate re-evaluates
// policy on the latest stored remediation plan immediately before approving. A
// higher-attempt run carrying a now-denied (display-only) plan must block an
// approval even if an earlier attempt was allowed.
func TestApproveRechecksPolicyOnStoredPlan(t *testing.T) {
	svc, aiopsSvc, runsRepo, auditRepo := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)
	// Attempt 1 was an allowed structured plan.
	storeRemediationRunAt(t, runsRepo, inc.ID, 1, aiops.RemediationAction{
		Command: "suspend cronjob", Reason: "r", Risk: "low",
		Kind: "suspend_cronjob", Namespace: inc.Namespace,
		ResourceKind: "CronJob", ResourceName: "job-0",
	})
	// Attempt 2 (latest) is now display-only and therefore denied.
	storeRemediationRunAt(t, runsRepo, inc.ID, 2, aiops.RemediationAction{
		Command: "manual fix only", Reason: "r", Risk: "low",
	})

	if err := svc.Approve(ctx, inc.ID, "operator", "plan is safe"); !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("err=%v want ErrActionNotAllowed (recheck must deny latest plan)", err)
	}
	got, _ := aiopsSvc.Get(ctx, inc.ID)
	if got.Status != aiops.StatusAwaitingApproval {
		t.Fatalf("status=%s want awaiting_approval", got.Status)
	}
	records, _ := auditRepo.List(ctx)
	if n := countAudit(records, "approve-remediation"); n != 0 {
		t.Fatalf("denied approve must not audit, got %d", n)
	}
}
