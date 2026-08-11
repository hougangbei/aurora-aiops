package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func openDeploymentEventsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "deployment-events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedDeploymentEventTask(t *testing.T, db *sql.DB, taskID string) {
	t.Helper()
	credID := "cred-" + taskID
	serverID := "server-" + taskID
	if _, err := db.Exec(`INSERT INTO asset_credentials (id,auth_type,nonce,ciphertext,created_at,updated_at) VALUES (?, 'password', x'01', x'02', 'now', 'now')`, credID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_servers (id,name,address,ssh_port,username,credential_id,status,created_at,updated_at) VALUES (?, ?, '192.0.2.1', 22, 'root', ?, 'online', 'now', 'now')`, serverID, serverID, credID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deployment_tasks (id,server_id,project_id,version,action,status,actor,config_nonce,config_ciphertext,created_at,updated_at) VALUES (?, ?, 'project-a','1.0.0','install','queued','operator',x'01',x'02','now','now')`, taskID, serverID); err != nil {
		t.Fatal(err)
	}
}

func TestEventStoreAppendListAfterAndReplay(t *testing.T) {
	db := openDeploymentEventsDB(t)
	s := NewEventStore(db)
	ctx := context.Background()

	seedDeploymentEventTask(t, db, "task-a")
	var ids []int64
	for i := 0; i < 3; i++ {
		ev, err := s.Append(ctx, "task-a", "log", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ev.ID)
	}
	if ids[0] <= 0 || ids[1] <= ids[0] || ids[2] <= ids[1] {
		t.Fatalf("event ids=%v are not increasing", ids)
	}
	all, err := s.ListAfter(ctx, "task-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != ids[0] || all[2].ID != ids[2] {
		t.Fatalf("events=%+v", all)
	}
	after, err := s.ListAfter(ctx, "task-a", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].ID != ids[1] {
		t.Fatalf("events after first=%+v", after)
	}
	other, err := s.ListAfter(ctx, "task-b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("events for another task=%+v", other)
	}

}

func TestEventStoreReplaysAfterSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedDeploymentEventTask(t, db, "task-a")
	s := NewEventStore(db)
	if _, err := s.Append(context.Background(), "task-a", "log", json.RawMessage(`{"persisted":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := NewEventStore(db).ListAfter(context.Background(), "task-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || string(events[0].Payload) != `{"persisted":true}` {
		t.Fatalf("replayed events=%+v", events)
	}
}

func TestEventStoreSubscriptionReceivesAfterCommit(t *testing.T) {
	db := openDeploymentEventsDB(t)
	s := NewEventStore(db)
	seedDeploymentEventTask(t, db, "task-a")
	ch, unsubscribe := s.Subscribe("task-a")
	defer unsubscribe()
	if _, err := s.Append(context.Background(), "task-a", "log", json.RawMessage(`{"message":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Type != "log" || string(ev.Payload) != `{"message":"ready"}` {
			t.Fatalf("event=%+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestEventStoreSlowSubscriberDoesNotBlockAndUnsubscribeRemovesIt(t *testing.T) {
	db := openDeploymentEventsDB(t)
	s := NewEventStore(db)
	seedDeploymentEventTask(t, db, "task-a")
	ch, unsubscribe := s.Subscribe("task-a")
	for i := 0; i < 256; i++ {
		if _, err := s.Append(context.Background(), "task-a", "log", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	if _, err := s.Append(context.Background(), "task-a", "log", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("slow subscriber blocked append: %s", elapsed)
	}
	unsubscribe()
	if _, err := s.Append(context.Background(), "task-a", "log", json.RawMessage(`{"after":true}`)); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case ev := <-ch:
			if string(ev.Payload) == `{"after":true}` {
				t.Fatal("received event after unsubscribe")
			}
		default:
			return
		}
	}
}
