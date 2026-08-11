package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Event is one durable deployment event. Payload is always valid JSON and is
// safe to expose to the progress API after the caller has redacted it.
type Event struct {
	ID        int64           `json:"id"`
	TaskID    string          `json:"taskId"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// EventStore persists deployment events and fans out newly committed events to
// live subscribers. Subscriber delivery is deliberately best effort; history
// remains authoritative in SQLite and can be replayed with ListAfter.
type EventStore struct {
	db      *sql.DB
	mu      sync.Mutex
	subs    map[string]map[int64]chan Event
	nextSub int64
}

// NewEventStore returns an event store bound to db.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db, subs: make(map[string]map[int64]chan Event)}
}

// Append inserts an event before notifying subscribers. typ is normally a
// string; EventInput is also accepted so callers that already build transition
// events can pass it directly. payload may be a json.RawMessage, []byte, or
// string containing a JSON document; an omitted or empty payload is stored as {}.
func (s *EventStore) Append(ctx context.Context, taskID string, typ any, payload ...any) (Event, error) {
	eventType, payload, err := normalizeEventArgs(typ, payload)
	if taskID == "" || eventType == "" {
		return Event{}, ErrInvalidInput
	}
	data, err := eventPayload(payload)
	if err != nil {
		return Event{}, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO deployment_events (task_id, event_type, payload, created_at)
		VALUES (?, ?, ?, ?)`, taskID, eventType, string(data), now.Format(time.RFC3339Nano))
	if err != nil {
		return Event{}, fmt.Errorf("persist deployment event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("deployment event id: %w", err)
	}
	ev := Event{ID: id, TaskID: taskID, Type: eventType, Payload: data, CreatedAt: now}
	// This happens only after the INSERT has succeeded, so a wake-up never
	// advertises an event that cannot be replayed from the database.
	s.broadcast(ev)
	return ev, nil
}

func normalizeEventArgs(typ any, payload []any) (string, []any, error) {
	switch value := typ.(type) {
	case string:
		return value, payload, nil
	case EventInput:
		if len(payload) != 0 {
			return "", nil, ErrInvalidInput
		}
		return value.Type, []any{value.Payload}, nil
	case *EventInput:
		if value == nil || len(payload) != 0 {
			return "", nil, ErrInvalidInput
		}
		return value.Type, []any{value.Payload}, nil
	default:
		return "", nil, ErrInvalidInput
	}
}

func eventPayload(payload []any) (json.RawMessage, error) {
	if len(payload) == 0 || payload[0] == nil {
		return json.RawMessage(`{}`), nil
	}
	if len(payload) != 1 {
		return nil, ErrInvalidInput
	}
	var data []byte
	switch value := payload[0].(type) {
	case json.RawMessage:
		data = append([]byte(nil), value...)
	case []byte:
		data = append([]byte(nil), value...)
	case string:
		data = []byte(value)
	default:
		return nil, ErrInvalidInput
	}
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	if !json.Valid(data) {
		return nil, ErrInvalidInput
	}
	return json.RawMessage(data), nil
}

// ListAfter returns task events whose id is strictly greater than afterID in
// ascending order. It is safe to call after reopening the SQLite database.
func (s *EventStore) ListAfter(ctx context.Context, taskID string, afterID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, event_type, payload, created_at
		FROM deployment_events WHERE task_id=? AND id>? ORDER BY id ASC`, taskID, afterID)
	if err != nil {
		return nil, fmt.Errorf("query deployment events: %w", err)
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var ev Event
		var payload, created string
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.Type, &payload, &created); err != nil {
			return nil, fmt.Errorf("scan deployment event: %w", err)
		}
		if !json.Valid([]byte(payload)) {
			return nil, fmt.Errorf("invalid deployment event payload for %d", ev.ID)
		}
		ev.Payload = json.RawMessage(payload)
		ev.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse deployment event time: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployment events: %w", err)
	}
	return events, nil
}

// Subscribe returns a buffered live-event channel and an idempotent removal
// function. Unsubscribe does not close the channel: a concurrent publisher may
// already have a value queued, and replay through ListAfter is the lifecycle
// boundary for clients.
func (s *EventStore) Subscribe(taskID string) (<-chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan Event, 32)
	if s.subs[taskID] == nil {
		s.subs[taskID] = make(map[int64]chan Event)
	}
	s.subs[taskID][id] = ch
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if taskSubs := s.subs[taskID]; taskSubs != nil {
				delete(taskSubs, id)
				if len(taskSubs) == 0 {
					delete(s.subs, taskID)
				}
			}
		})
	}
	return ch, unsubscribe
}

func (s *EventStore) broadcast(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subs[ev.TaskID] {
		select {
		case ch <- ev:
		default:
			// A slow subscriber must never hold up a worker or database writer.
		}
	}
}

// Wake notifies live consumers that durable state may have changed. Worker
// repository transitions write directly in their transaction, so the SSE
// handler always replays SQLite after this best-effort wake-up.
func (s *EventStore) Wake(taskID string) {
	if s == nil || taskID == "" {
		return
	}
	s.broadcast(Event{TaskID: taskID})
}
