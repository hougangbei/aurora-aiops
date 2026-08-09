package store

import (
	"database/sql"
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

func TestOpenCreatesEvidenceSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, want := range []string{"evidence_nodes", "evidence_edges"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, want).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", want, err)
		}
		if name != want {
			t.Fatalf("table=%q want %q", name, want)
		}
	}
}

func TestOpenCreatesAssetSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, want := range []string{
		"asset_credentials",
		"asset_servers",
		"asset_snapshots",
		"asset_software_items",
		"project_installations",
	} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, want).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", want, err)
		}
		if name != want {
			t.Fatalf("table=%q want %q", name, want)
		}
	}

	var indexName string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_asset_snapshots_server_time'`).Scan(&indexName)
	if err != nil {
		t.Fatal(err)
	}
	if indexName != "idx_asset_snapshots_server_time" {
		t.Fatalf("index=%q want idx_asset_snapshots_server_time", indexName)
	}
}

func TestAssetSchemaMigrationIsIdempotent(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(`
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, created_at, updated_at)
VALUES ('credential-1', 'password', x'01', x'02', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	if err := migrateAssetSchema(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_credentials WHERE id='credential-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("credential count=%d want 1", count)
	}
}

func TestAssetSchemaEnforcesChecksAndForeignKeys(t *testing.T) {
	db := openTestDB(t)

	assertExecFails(t, db, `
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, created_at, updated_at)
VALUES ('invalid-credential', 'token', x'01', x'02', 'now', 'now')`)

	if _, err := db.Exec(`
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, created_at, updated_at)
VALUES ('credential-1', 'private_key', x'01', x'02', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		id     string
		port   int
		status string
		credID string
	}{
		{name: "port below range", id: "server-port-low", port: 0, status: "pending", credID: "credential-1"},
		{name: "port above range", id: "server-port-high", port: 65536, status: "pending", credID: "credential-1"},
		{name: "invalid status", id: "server-status", port: 22, status: "unknown", credID: "credential-1"},
		{name: "missing credential", id: "server-credential", port: 22, status: "pending", credID: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`
INSERT INTO asset_servers (id, name, address, ssh_port, username, credential_id, status, created_at, updated_at)
VALUES (?, ?, '192.0.2.1', ?, 'root', ?, ?, 'now', 'now')`, tc.id, tc.id, tc.port, tc.credID, tc.status)
			if err == nil {
				t.Fatal("expected insert to fail")
			}
		})
	}
}

func TestAssetSchemaCascadesAndInstallationUniqueness(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.Exec(`
INSERT INTO asset_credentials (id, auth_type, nonce, ciphertext, created_at, updated_at)
VALUES ('credential-1', 'password', x'01', x'02', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO asset_servers (id, name, address, ssh_port, username, credential_id, status, created_at, updated_at)
VALUES ('server-1', 'server-1', '192.0.2.1', 22, 'root', 'credential-1', 'online', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO asset_snapshots (id, server_id, payload, collected_at)
VALUES ('snapshot-1', 'server-1', '{}', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO asset_software_items (snapshot_id, category, name)
VALUES ('snapshot-1', 'system_package', 'curl')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO project_installations (id, server_id, project_id, version, status, created_at, updated_at)
VALUES ('installation-1', 'server-1', 'project-1', '1.0.0', 'installed', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	assertExecFails(t, db, `
INSERT INTO project_installations (id, server_id, project_id, version, status, created_at, updated_at)
VALUES ('installation-2', 'server-1', 'project-1', '2.0.0', 'installed', 'now', 'now')`)
	assertExecFails(t, db, `DELETE FROM asset_credentials WHERE id='credential-1'`)

	if _, err := db.Exec(`DELETE FROM asset_servers WHERE id='server-1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"asset_snapshots", "asset_software_items", "project_installations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count=%d want 0 after server deletion", table, count)
		}
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func assertExecFails(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err == nil {
		t.Fatalf("expected query to fail: %s", query)
	}
}
