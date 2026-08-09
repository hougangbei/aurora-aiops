package aiops

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/heihuzicity-tech/aurora-aiops/server/internal/store"
)

func newTestRepository(t *testing.T) IncidentRepository {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRepository(db)
}

func TestIncidentRepositoryCreateAndGet(t *testing.T) {
	repo := newTestRepository(t)
	want := Incident{
		ID:           "inc-001",
		Summary:      "Pod crash loop",
		Severity:     SeverityCritical,
		Status:       StatusReceived,
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
		CreatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
	if err := repo.Create(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}

func TestIncidentRepositoryGetNotFound(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrIncidentNotFound) {
		t.Fatalf("got err=%v, want ErrIncidentNotFound", err)
	}
}

func TestIncidentRepositoryListOrder(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	for i, id := range []string{"inc-a", "inc-b", "inc-c"} {
		inc := Incident{
			ID:           id,
			Summary:      "test " + id,
			Severity:     SeverityInfo,
			Status:       StatusReceived,
			Namespace:    "default",
			ResourceKind: "Pod",
			ResourceName: "pod-" + id,
			CreatedAt:    base,
			UpdatedAt:    base.Add(time.Duration(i) * time.Hour),
		}
		if err := repo.Create(ctx, inc); err != nil {
			t.Fatal(err)
		}
	}

	list, err := repo.List(ctx, IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d, want 3", len(list))
	}
	// ORDER BY updated_at DESC => most recently updated first
	if list[0].ID != "inc-c" || list[1].ID != "inc-b" || list[2].ID != "inc-a" {
		t.Fatalf("order=%v, want [inc-c inc-b inc-a]", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

func TestIncidentRepositoryListEmpty(t *testing.T) {
	repo := newTestRepository(t)
	list, err := repo.List(context.Background(), IncidentFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("len=%d, want 0", len(list))
	}
}

func TestIncidentRepositoryListFilterByStatus(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	for _, inc := range []Incident{
		{ID: "inc-1", Summary: "a", Severity: SeverityInfo, Status: StatusReceived, Namespace: "default", ResourceKind: "Pod", ResourceName: "p1", CreatedAt: base, UpdatedAt: base},
		{ID: "inc-2", Summary: "b", Severity: SeverityWarning, Status: StatusTriaging, Namespace: "default", ResourceKind: "Pod", ResourceName: "p2", CreatedAt: base, UpdatedAt: base},
	} {
		if err := repo.Create(ctx, inc); err != nil {
			t.Fatal(err)
		}
	}

	list, err := repo.List(ctx, IncidentFilter{Status: StatusTriaging})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "inc-2" {
		t.Fatalf("list=%v, want [inc-2]", list)
	}
}

func TestIncidentRepositoryListFilterByNamespace(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	for _, inc := range []Incident{
		{ID: "inc-1", Summary: "a", Severity: SeverityInfo, Status: StatusReceived, Namespace: "default", ResourceKind: "Pod", ResourceName: "p1", CreatedAt: base, UpdatedAt: base},
		{ID: "inc-2", Summary: "b", Severity: SeverityWarning, Status: StatusReceived, Namespace: "kube-system", ResourceKind: "Pod", ResourceName: "p2", CreatedAt: base, UpdatedAt: base},
	} {
		if err := repo.Create(ctx, inc); err != nil {
			t.Fatal(err)
		}
	}

	list, err := repo.List(ctx, IncidentFilter{Namespace: "kube-system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "inc-2" {
		t.Fatalf("list=%v, want [inc-2]", list)
	}
}

func TestIncidentRepositoryUpdateStatus(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	inc := Incident{
		ID:           "inc-1",
		Summary:      "test",
		Severity:     SeverityCritical,
		Status:       StatusReceived,
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
		CreatedAt:    base,
		UpdatedAt:    base,
	}
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatal(err)
	}

	newTime := base.Add(2 * time.Hour)
	if err := repo.UpdateStatus(ctx, "inc-1", StatusTriaging, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusTriaging {
		t.Fatalf("status=%q, want %q", got.Status, StatusTriaging)
	}
	if !got.UpdatedAt.Equal(newTime) {
		t.Fatalf("updatedAt=%v, want %v", got.UpdatedAt, newTime)
	}
}

func TestIncidentRepositoryUpdateStatusNotFound(t *testing.T) {
	repo := newTestRepository(t)
	err := repo.UpdateStatus(context.Background(), "nonexistent", StatusTriaging, time.Now())
	if !errors.Is(err, ErrIncidentNotFound) {
		t.Fatalf("got err=%v, want ErrIncidentNotFound", err)
	}
}

func TestRepositoryTransitionStatusCompareAndSwap(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	now := time.Now().UTC()

	inc := Incident{
		ID: "inc-cas", Summary: "cas", Severity: SeverityCritical,
		Status: StatusAwaitingApproval, Namespace: "default",
		ResourceKind: "Pod", ResourceName: "api-0",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatal(err)
	}

	if err := repo.TransitionStatus(ctx, "inc-cas", StatusAwaitingApproval, StatusApproved, now); err != nil {
		t.Fatalf("first transition: %v", err)
	}
	// Status is now approved: a second transition claiming awaiting_approval must conflict.
	if err := repo.TransitionStatus(ctx, "inc-cas", StatusAwaitingApproval, StatusRejected, now); !errors.Is(err, ErrStateTransitionConflict) {
		t.Fatalf("err=%v want ErrStateTransitionConflict", err)
	}
	// Missing incident: not-found, not conflict.
	if err := repo.TransitionStatus(ctx, "missing", StatusAwaitingApproval, StatusRejected, now); !errors.Is(err, ErrIncidentNotFound) {
		t.Fatalf("err=%v want ErrIncidentNotFound", err)
	}
}

func TestIncidentRepositoryCreateDuplicateID(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	inc := Incident{
		ID:           "inc-dup",
		Summary:      "first",
		Severity:     SeverityInfo,
		Status:       StatusReceived,
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "p1",
		CreatedAt:    base,
		UpdatedAt:    base,
	}
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatal(err)
	}

	inc.Summary = "second"
	err := repo.Create(ctx, inc)
	if !errors.Is(err, ErrIncidentAlreadyExists) {
		t.Fatalf("got err=%v, want ErrIncidentAlreadyExists", err)
	}
}
