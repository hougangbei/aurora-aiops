package audit

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

func newAuditTest(t *testing.T) (*sql.DB, Repository) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewRepository(db)
}

func sampleRecord(actor, action string, ts time.Time) Record {
	return Record{
		Actor:     actor,
		Action:    action,
		Target:    "inc-1",
		Result:    "success",
		Payload:   `{"k":"v"}`,
		Timestamp: ts,
	}
}

func appendThree(t *testing.T, repo Repository) []Record {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	var records []Record
	for i := 0; i < 3; i++ {
		record, err := repo.Append(ctx, sampleRecord("admin", "execution_started", base.Add(time.Duration(i)*time.Second)))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		records = append(records, record)
	}
	return records
}

func TestAppendAndVerifyChain(t *testing.T) {
	_, repo := newAuditTest(t)
	appendThree(t, repo)

	records, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("records=%d want 3", len(records))
	}
	for _, record := range records {
		if record.Hash == "" {
			t.Fatalf("record %d has empty hash", record.ID)
		}
	}
	if err := Verify(records); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyDetectsInMemoryTampering(t *testing.T) {
	_, repo := newAuditTest(t)
	appendThree(t, repo)

	records, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	records[1].Payload = `{"k":"tampered"}`

	if err := Verify(records); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestVerifyDetectsDBTampering(t *testing.T) {
	db, repo := newAuditTest(t)
	appendThree(t, repo)

	// Direct DB update simulates an attacker rewriting an old record.
	if _, err := db.Exec(`UPDATE audit_records SET payload = ? WHERE id = ?`, `{"k":"tampered"}`, 2); err != nil {
		t.Fatal(err)
	}

	records, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(records); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestPayloadRedactedBeforeStore(t *testing.T) {
	_, repo := newAuditTest(t)
	ctx := context.Background()

	record := sampleRecord("admin", "approve", time.Now().UTC())
	record.Payload = `{"token":"eyJhbGciOiJIUzI1NiJ9.secretsecretsecretsecret","apiKey":"sk-test-abcdef","note":"ok"}`
	stored, err := repo.Append(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Payload, "eyJhbGciOiJIUzI1NiJ9") ||
		strings.Contains(stored.Payload, "sk-test-abcdef") {
		t.Fatalf("credential leaked in audit payload: %s", stored.Payload)
	}
	if !strings.Contains(stored.Payload, "[REDACTED]") {
		t.Fatalf("no redaction marker in audit payload: %s", stored.Payload)
	}
}

func TestVerifyEmptyChainPasses(t *testing.T) {
	if err := Verify(nil); err != nil {
		t.Fatalf("Verify(empty) = %v", err)
	}
}
