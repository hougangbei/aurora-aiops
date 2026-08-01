package store

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesIncidentSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='incidents'`).Scan(&name)
	if err != nil {
		t.Fatal(err)
	}
	if name != "incidents" {
		t.Fatalf("table=%q", name)
	}
}
