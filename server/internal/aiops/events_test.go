package aiops

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func newEventStoreTest(t *testing.T) (*EventStore, *Service) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewEventStore(db), NewService(NewRepository(db))
}

func mustCreateIncidentForEvents(t *testing.T, svc *Service, name string) Incident {
	t.Helper()
	inc, err := svc.Create(context.Background(), CreateIncidentInput{
		Summary:      "pod " + name,
		Severity:     "info",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: name,
	})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}
	return inc
}

func TestEventStoreAppendAndListAfter(t *testing.T) {
	s, svc := newEventStoreTest(t)
	ctx := context.Background()
	inc := mustCreateIncidentForEvents(t, svc, "api-0")

	var ids []int64
	for i := 0; i < 3; i++ {
		ev, err := s.Append(ctx, inc.ID, EventRunStarted, fmt.Sprintf(`{"i":%d}`, i))
		if err != nil {
			t.Fatal(err)
		}
		if ev.ID == 0 {
			t.Fatal("expected non-zero persisted id")
		}
		ids = append(ids, ev.ID)
	}

	// all events after 0
	all, err := s.ListAfter(ctx, inc.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("events=%d want 3", len(all))
	}
	// ascending ids
	for i, ev := range all {
		if ev.ID != ids[i] {
			t.Fatalf("all[%d].id=%d want %d", i, ev.ID, ids[i])
		}
	}

	// resume after the first event
	after, err := s.ListAfter(ctx, inc.ID, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].ID != ids[1] {
		t.Fatalf("resume after=%+v want 2 events starting at %d", after, ids[1])
	}

	// incidents are isolated
	other := mustCreateIncidentForEvents(t, svc, "other")
	otherEvents, err := s.ListAfter(ctx, other.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherEvents) != 0 {
		t.Fatalf("other incident events=%d want 0", len(otherEvents))
	}
}

func TestEventStoreBroadcastToSubscriber(t *testing.T) {
	s, svc := newEventStoreTest(t)
	ctx := context.Background()
	inc := mustCreateIncidentForEvents(t, svc, "api-0")

	ch, unsubscribe := s.Subscribe(inc.ID)
	defer unsubscribe()

	if _, err := s.Append(ctx, inc.ID, EventRunCompleted, `{"status":"succeeded"}`); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Type != EventRunCompleted {
			t.Fatalf("type=%s want run_completed", ev.Type)
		}
		if ev.Data != `{"status":"succeeded"}` {
			t.Fatalf("data=%s", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive broadcast")
	}
}

func TestEventStoreUnsubscribeStopsDelivery(t *testing.T) {
	s, svc := newEventStoreTest(t)
	ctx := context.Background()
	inc := mustCreateIncidentForEvents(t, svc, "api-0")

	ch, unsubscribe := s.Subscribe(inc.ID)
	unsubscribe()
	if _, err := s.Append(ctx, inc.ID, EventRunStarted, "{}"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		t.Fatalf("received after unsubscribe: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestEventStoreSlowSubscriberDoesNotBlock(t *testing.T) {
	s, svc := newEventStoreTest(t)
	ctx := context.Background()
	inc := mustCreateIncidentForEvents(t, svc, "api-0")

	// Subscriber that never reads: the 32-slot buffer fills and further events
	// must be dropped, never blocking Append.
	_, _ = s.Subscribe(inc.ID)

	start := time.Now()
	for i := 0; i < 300; i++ {
		if _, err := s.Append(ctx, inc.ID, EventRunStarted, fmt.Sprintf(`{"i":%d}`, i)); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("append blocked on slow subscriber: %s", elapsed)
	}
}

func TestEventStoreSubscribersAreIncidentScoped(t *testing.T) {
	s, svc := newEventStoreTest(t)
	ctx := context.Background()
	incA := mustCreateIncidentForEvents(t, svc, "a")
	incB := mustCreateIncidentForEvents(t, svc, "b")

	ch, unsubscribe := s.Subscribe(incA.ID)
	defer unsubscribe()

	if _, err := s.Append(ctx, incB.ID, EventRunStarted, "{}"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		t.Fatalf("received event for another incident: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}
