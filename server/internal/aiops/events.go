package aiops

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// EventType categorizes incident events streamed over SSE.
type EventType string

const (
	EventRunStarted       EventType = "run_started"
	EventRunCompleted     EventType = "run_completed"
	EventIncidentUpdated  EventType = "incident_updated"
	EventReanalyzeStarted EventType = "reanalyze_started"
)

// Event is one persisted incident event with a monotonic database id.
type Event struct {
	ID         int64     `json:"id"`
	IncidentID string    `json:"incidentId"`
	Type       EventType `json:"type"`
	Data       string    `json:"data"`
	CreatedAt  time.Time `json:"createdAt"`
}

// EventStore persists events to SQLite and fans them out to live subscribers.
// Events are written before being broadcast, so history is never lost to a slow
// client: it can reconnect with Last-Event-ID and replay from the database.
type EventStore struct {
	db      *sql.DB
	mu      sync.Mutex
	subs    map[string]map[int64]chan Event
	nextSub int64
}

// NewEventStore returns an EventStore bound to db.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db, subs: make(map[string]map[int64]chan Event)}
}

// Append persists the event, then broadcasts it to live subscribers. The write
// succeeds regardless of subscriber behavior.
func (s *EventStore) Append(ctx context.Context, incidentID string, typ EventType, data string) (Event, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_events (incident_id, type, data, created_at) VALUES (?, ?, ?, ?)`,
		incidentID, string(typ), data, now.Format(time.RFC3339Nano))
	if err != nil {
		return Event{}, fmt.Errorf("persist incident event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("incident event id: %w", err)
	}
	ev := Event{ID: id, IncidentID: incidentID, Type: typ, Data: data, CreatedAt: now}
	s.broadcast(ev)
	return ev, nil
}

// Subscribe returns a buffered channel of live events for the incident plus an
// idempotent unsubscribe function. The channel is never closed by the store; a
// client that stops reading simply stops receiving (its history stays in the
// database for replay).
func (s *EventStore) Subscribe(incidentID string) (<-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan Event, 32)
	if s.subs[incidentID] == nil {
		s.subs[incidentID] = make(map[int64]chan Event)
	}
	s.subs[incidentID][id] = ch
	unsubscribe := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[incidentID][id]; ok {
			delete(s.subs[incidentID], id)
		}
	}
	return ch, unsubscribe
}

// broadcast delivers to every subscriber of the incident with a non-blocking
// send. A subscriber whose buffer is full drops the live event instead of
// stalling the workflow; recovery happens through Last-Event-ID replay.
func (s *EventStore) broadcast(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subs[ev.IncidentID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ListAfter returns persisted events for the incident with id strictly greater
// than afterID, in ascending order. This powers Last-Event-ID resume.
func (s *EventStore) ListAfter(ctx context.Context, incidentID string, afterID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id, type, data, created_at FROM incident_events
		WHERE incident_id = ? AND id > ? ORDER BY id ASC`, incidentID, afterID)
	if err != nil {
		return nil, fmt.Errorf("query incident events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		var typ, createdAtStr string
		if err := rows.Scan(&ev.ID, &ev.IncidentID, &typ, &ev.Data, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan incident event: %w", err)
		}
		ev.Type = EventType(typ)
		if ev.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr); err != nil {
			return nil, fmt.Errorf("parse incident event time: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident events: %w", err)
	}
	return events, nil
}
