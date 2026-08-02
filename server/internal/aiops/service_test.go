package aiops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRepository implements IncidentRepository for testing.
// It records calls to Create and UpdateStatus so tests can verify
// that the Service does not call the repository on invalid operations.
type fakeRepository struct {
	mu sync.Mutex

	incidents map[string]Incident

	// getError, when non-nil, is returned by Get instead of looking up incidents.
	// This allows tests to inject repository-level errors (e.g. DB connection lost).
	getError error

	createCalls       int
	updateStatusCalls int
	lastUpdateID      string
	lastUpdateStatus  Status
	lastUpdateTime    time.Time

	transitionStatusCalls int
	lastTransitionFrom    Status
	lastTransitionTo      Status
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{incidents: make(map[string]Incident)}
}

func (f *fakeRepository) Create(_ context.Context, inc Incident) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incidents[inc.ID] = inc
	f.createCalls++
	return nil
}

func (f *fakeRepository) Get(_ context.Context, id string) (Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getError != nil {
		return Incident{}, f.getError
	}
	inc, ok := f.incidents[id]
	if !ok {
		return Incident{}, ErrIncidentNotFound
	}
	return inc, nil
}

func (f *fakeRepository) List(_ context.Context, filter IncidentFilter) ([]Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []Incident
	for _, inc := range f.incidents {
		if filter.Status != "" && inc.Status != filter.Status {
			continue
		}
		if filter.Namespace != "" && inc.Namespace != filter.Namespace {
			continue
		}
		result = append(result, inc)
	}
	return result, nil
}

func (f *fakeRepository) UpdateStatus(_ context.Context, id string, status Status, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	inc, ok := f.incidents[id]
	if !ok {
		return ErrIncidentNotFound
	}
	inc.Status = status
	inc.UpdatedAt = updatedAt
	f.incidents[id] = inc
	f.updateStatusCalls++
	f.lastUpdateID = id
	f.lastUpdateStatus = status
	f.lastUpdateTime = updatedAt
	return nil
}

func (f *fakeRepository) TransitionStatus(_ context.Context, id string, from, to Status, updatedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	inc, ok := f.incidents[id]
	if !ok {
		return ErrIncidentNotFound
	}
	if inc.Status != from {
		return ErrStateTransitionConflict
	}
	inc.Status = to
	inc.UpdatedAt = updatedAt
	f.incidents[id] = inc
	f.transitionStatusCalls++
	f.lastTransitionFrom = from
	f.lastTransitionTo = to
	return nil
}

func TestAdvanceValidTransition(t *testing.T) {
	fake := newFakeRepository()
	inc := Incident{
		ID: "inc-test", Summary: "test", Severity: SeverityCritical,
		Status: StatusReceived, Namespace: "default",
		ResourceKind: "Pod", ResourceName: "api-0",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	fake.incidents[inc.ID] = inc

	svc := NewService(fake)
	err := svc.Advance(context.Background(), inc.ID, StatusTriaging)
	if err != nil {
		t.Fatalf("Advance valid transition: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.transitionStatusCalls != 1 {
		t.Errorf("TransitionStatus called %d times, want 1", fake.transitionStatusCalls)
	}
	if fake.lastTransitionTo != StatusTriaging {
		t.Errorf("lastTransitionTo = %q, want %q", fake.lastTransitionTo, StatusTriaging)
	}
}

func TestAdvanceInvalidTransition(t *testing.T) {
	fake := newFakeRepository()
	inc := Incident{
		ID: "inc-test", Summary: "test", Severity: SeverityCritical,
		Status: StatusReceived, Namespace: "default",
		ResourceKind: "Pod", ResourceName: "api-0",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	fake.incidents[inc.ID] = inc

	svc := NewService(fake)
	err := svc.Advance(context.Background(), inc.ID, StatusResolved)
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("Advance invalid transition: got err=%v, want ErrInvalidStateTransition", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.transitionStatusCalls != 0 {
		t.Errorf("TransitionStatus called %d times, want 0", fake.transitionStatusCalls)
	}
}

func TestAdvanceNotFound(t *testing.T) {
	fake := newFakeRepository()
	svc := NewService(fake)

	err := svc.Advance(context.Background(), "inc-nonexistent", StatusTriaging)
	if !errors.Is(err, ErrIncidentNotFound) {
		t.Fatalf("Advance not found: got err=%v, want ErrIncidentNotFound", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.transitionStatusCalls != 0 {
		t.Errorf("TransitionStatus called %d times, want 0", fake.transitionStatusCalls)
	}
}

func TestAdvanceRepoGetError(t *testing.T) {
	dbErr := errors.New("db connection lost")
	fake := newFakeRepository()
	fake.getError = dbErr

	svc := NewService(fake)
	err := svc.Advance(context.Background(), "inc-any", StatusTriaging)
	if err == nil {
		t.Fatal("Advance: got nil error, want non-nil")
	}
	if errors.Is(err, ErrIncidentNotFound) {
		t.Fatalf("Advance: got ErrIncidentNotFound, want raw DB error")
	}
	if errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("Advance: got ErrInvalidStateTransition, want raw DB error")
	}
	if err.Error() != dbErr.Error() {
		t.Fatalf("Advance: got err=%v, want err=%v", err, dbErr)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.transitionStatusCalls != 0 {
		t.Errorf("TransitionStatus called %d times, want 0", fake.transitionStatusCalls)
	}
}

func TestCreateSuccess(t *testing.T) {
	fake := newFakeRepository()
	svc := NewService(fake)

	inc, err := svc.Create(context.Background(), CreateIncidentInput{
		Summary:      "Pod crash loop",
		Severity:     "critical",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inc.ID == "" {
		t.Error("ID is empty")
	}
	if !strings.HasPrefix(inc.ID, "inc-") {
		t.Errorf("ID = %q, want prefix 'inc-'", inc.ID)
	}
	if inc.Status != StatusReceived {
		t.Errorf("Status = %q, want %q", inc.Status, StatusReceived)
	}
	if inc.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if inc.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
	if inc.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want %q", inc.Severity, SeverityCritical)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.createCalls != 1 {
		t.Errorf("Create called %d times, want 1", fake.createCalls)
	}
}

func TestCreateInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input CreateIncidentInput
	}{
		{"empty summary", CreateIncidentInput{
			Summary: "", Severity: "critical", Namespace: "default",
			ResourceKind: "Pod", ResourceName: "api-0",
		}},
		{"invalid severity", CreateIncidentInput{
			Summary: "test", Severity: "unknown", Namespace: "default",
			ResourceKind: "Pod", ResourceName: "api-0",
		}},
		{"empty namespace", CreateIncidentInput{
			Summary: "test", Severity: "critical", Namespace: "",
			ResourceKind: "Pod", ResourceName: "api-0",
		}},
		{"empty resourceKind", CreateIncidentInput{
			Summary: "test", Severity: "critical", Namespace: "default",
			ResourceKind: "", ResourceName: "api-0",
		}},
		{"empty resourceName", CreateIncidentInput{
			Summary: "test", Severity: "critical", Namespace: "default",
			ResourceKind: "Pod", ResourceName: "",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeRepository()
			svc := NewService(fake)

			_, err := svc.Create(context.Background(), tt.input)
			if !errors.Is(err, ErrInvalidIncidentRequest) {
				t.Fatalf("got err=%v, want ErrInvalidIncidentRequest", err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.createCalls != 0 {
				t.Errorf("Create called %d times, want 0", fake.createCalls)
			}
		})
	}
}

func TestDecideConcurrentDecisionHasOneWinner(t *testing.T) {
	fake := newFakeRepository()
	now := time.Now().UTC()
	inc := Incident{
		ID: "inc-concurrent", Summary: "race", Severity: SeverityCritical,
		Status: StatusAwaitingApproval, Namespace: "default",
		ResourceKind: "Pod", ResourceName: "api-0",
		CreatedAt: now, UpdatedAt: now,
	}
	fake.incidents[inc.ID] = inc
	svc := NewService(fake)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var approveErr, rejectErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		approveErr = svc.Decide(context.Background(), inc.ID, StatusApproved)
	}()
	go func() {
		defer wg.Done()
		<-start
		rejectErr = svc.Decide(context.Background(), inc.ID, StatusRejected)
	}()
	close(start)
	wg.Wait()

	nilCount, conflictCount := 0, 0
	for _, err := range []error{approveErr, rejectErr} {
		if err == nil {
			nilCount++
		}
		if errors.Is(err, ErrStateTransitionConflict) {
			conflictCount++
		}
	}
	if nilCount != 1 {
		t.Fatalf("nilCount=%d want 1 (approve=%v reject=%v)", nilCount, approveErr, rejectErr)
	}
	if conflictCount != 1 {
		t.Fatalf("conflictCount=%d want 1 (approve=%v reject=%v)", conflictCount, approveErr, rejectErr)
	}

	got, err := fake.Get(context.Background(), inc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusApproved && got.Status != StatusRejected {
		t.Fatalf("stored status=%s want approved or rejected", got.Status)
	}
	if fake.transitionStatusCalls != 1 {
		t.Fatalf("transitionStatusCalls=%d want exactly 1", fake.transitionStatusCalls)
	}
}
