package remediation

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/audit"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// newRemediationTestService wires a remediation Service against a real SQLite
// store. Approve/Reject exercise only the aiops CAS and the audit chain, so the
// executor, kube client and snapshot store are left nil.
func newRemediationTestService(t *testing.T) (*Service, *aiops.Service, audit.Repository) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "remediation.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	aiopsSvc := aiops.NewService(aiops.NewRepository(db))
	auditRepo := audit.NewRepository(db)
	svc := NewService(aiopsSvc, aiops.NewRunRepository(db), nil, nil, auditRepo, nil)
	return svc, aiopsSvc, auditRepo
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

// TestApproveRejectConcurrentDecisionSingleWinner proves the compare-and-swap
// decision path has exactly one winner under concurrent approve/reject: one call
// returns nil, the other ErrStateTransitionConflict, only one audit record is
// appended, and the stored status is a single terminal decision.
func TestApproveRejectConcurrentDecisionSingleWinner(t *testing.T) {
	svc, aiopsSvc, auditRepo := newRemediationTestService(t)
	ctx := context.Background()
	inc := awaitApprovalIncident(t, aiopsSvc)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var approveErr, rejectErr error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; approveErr = svc.Approve(ctx, inc.ID, "operator-a", "approved: plan is safe") }()
	go func() { defer wg.Done(); <-start; rejectErr = svc.Reject(ctx, inc.ID, "operator-b", "rejected: too risky") }()
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
	var decisions []audit.Record
	for _, r := range records {
		if r.Action == "approve-remediation" || r.Action == "reject-remediation" {
			decisions = append(decisions, r)
		}
	}
	if len(decisions) != 1 {
		t.Fatalf("decision audit records=%d want exactly 1 (loser must not audit)", len(decisions))
	}

	got, err := aiopsSvc.Get(ctx, inc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != aiops.StatusApproved && got.Status != aiops.StatusRejected {
		t.Fatalf("stored status=%s want a single terminal decision", got.Status)
	}
}
