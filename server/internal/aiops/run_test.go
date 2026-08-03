package aiops

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// TestListRunsOrdersByInsertion guards the rowid sort: started_at is stored as
// RFC3339Nano text whose trailing zeros are stripped, so a lexical timestamp
// sort is not monotonic (e.g. ".55Z" sorts before ".5Z") and can reorder runs
// created sequentially. Callers (latestRootCause, latestRemediation) iterate
// from the end assuming the final row is the most recent run.
func TestListRunsOrdersByInsertion(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	incRepo := NewRepository(db)
	runRepo := NewRunRepository(db)

	inc := Incident{
		ID:           "inc-ordering",
		Summary:      "ordering",
		Severity:     SeverityInfo,
		Status:       StatusReceived,
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "pod-ordering",
		CreatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
	if err := incRepo.Create(ctx, inc); err != nil {
		t.Fatal(err)
	}

	// 500ms stores as ".5Z" while 550ms stores as ".55Z"; lexically the later
	// run sorts first, so only a monotonic key (rowid) preserves creation order.
	makeRun := func(id, role string, at time.Time) {
		if err := runRepo.CreateRun(ctx, AgentRun{
			ID: id, IncidentID: inc.ID, Role: role, Attempt: 1,
			Status: RunStatusSucceeded, StartedAt: at, CompletedAt: at.Add(time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 8, 2, 10, 0, 0, int(500*time.Millisecond), time.UTC)
	makeRun("run-first", "triage", base)
	makeRun("run-second", "collector", base.Add(50*time.Millisecond))

	got, err := runRepo.ListRuns(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "run-first" || got[1].ID != "run-second" {
		t.Fatalf("order=%v want [run-first run-second]",
			[]string{got[0].ID, got[1].ID})
	}
}
