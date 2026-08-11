package store

import (
	"database/sql"
	"path/filepath"
	"strings"
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

func TestOpenCreatesDeploymentSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, want := range []string{
		"deployment_tasks",
		"deployment_steps",
		"deployment_events",
		"deployment_task_values",
	} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, want).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", want, err)
		}
		if name != want {
			t.Fatalf("table=%q want %q", name, want)
		}
	}

	var indexSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_deployment_tasks_one_active_server'`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	indexSQL = strings.Join(strings.Fields(indexSQL), " ")
	if !strings.Contains(indexSQL, "CREATE UNIQUE INDEX idx_deployment_tasks_one_active_server ON deployment_tasks(server_id) WHERE status IN ('queued','running')") {
		t.Fatalf("active-task index SQL=%q", indexSQL)
	}
	for _, want := range []string{"idx_deployment_tasks_status_created", "idx_deployment_events_task_id"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, want).Scan(&name); err != nil {
			t.Fatalf("index %s: %v", want, err)
		}
		if name != want {
			t.Fatalf("index=%q want %q", name, want)
		}
	}

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

	insertTask := func(t *testing.T, id, status string) {
		t.Helper()
		if _, err := db.Exec(`
INSERT INTO deployment_tasks (
  id, server_id, project_id, version, action, status, actor,
  config_nonce, config_ciphertext, created_at, updated_at
) VALUES (?, 'server-1', 'project-1', '1.0.0', 'install', ?, 'operator', x'01', x'02', 'now', 'now')`, id, status); err != nil {
			t.Fatal(err)
		}
	}

	insertTask(t, "task-1", "queued")
	assertExecFails(t, db, `
INSERT INTO deployment_tasks (
  id, server_id, project_id, version, action, status, actor,
  config_nonce, config_ciphertext, created_at, updated_at
) VALUES ('task-2', 'server-1', 'project-1', '1.0.0', 'install', 'running', 'operator', x'01', x'02', 'now', 'now')`)

	for _, query := range []string{
		`INSERT INTO deployment_tasks (id, server_id, project_id, version, action, status, actor, config_nonce, config_ciphertext, created_at, updated_at) VALUES ('invalid-action', 'server-1', 'project-1', '1.0.0', 'remove', 'succeeded', 'operator', x'01', x'02', 'now', 'now')`,
		`INSERT INTO deployment_tasks (id, server_id, project_id, version, action, status, actor, config_nonce, config_ciphertext, created_at, updated_at) VALUES ('invalid-status', 'server-1', 'project-1', '1.0.0', 'install', 'unknown', 'operator', x'01', x'02', 'now', 'now')`,
		`INSERT INTO deployment_tasks (id, server_id, project_id, version, action, status, actor, percent, config_nonce, config_ciphertext, created_at, updated_at) VALUES ('invalid-percent', 'server-1', 'project-1', '1.0.0', 'install', 'succeeded', 'operator', 101, x'01', x'02', 'now', 'now')`,
		`INSERT INTO deployment_tasks (id, server_id, project_id, version, action, status, actor, retry_of, config_nonce, config_ciphertext, created_at, updated_at) VALUES ('invalid-retry', 'server-1', 'project-1', '1.0.0', 'install', 'succeeded', 'operator', 'missing-task', x'01', x'02', 'now', 'now')`,
	} {
		assertExecFails(t, db, query)
	}

	if _, err := db.Exec(`
INSERT INTO deployment_steps (task_id, step_id, label, ordinal, percent, status)
VALUES ('task-1', 'prepare', 'Prepare server', 1, 10, 'pending')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO deployment_steps (task_id, step_id, label, ordinal, percent, status) VALUES ('task-1', 'invalid-percent', 'Invalid percent', 2, 0, 'pending')`,
		`INSERT INTO deployment_steps (task_id, step_id, label, ordinal, percent, status) VALUES ('task-1', 'invalid-status', 'Invalid status', 2, 20, 'unknown')`,
		`INSERT INTO deployment_steps (task_id, step_id, label, ordinal, percent, status) VALUES ('task-1', 'duplicate-ordinal', 'Duplicate ordinal', 1, 20, 'pending')`,
	} {
		assertExecFails(t, db, query)
	}

	if _, err := db.Exec(`INSERT INTO deployment_events (task_id, event_type, payload, created_at) VALUES ('task-1', 'created', '{}', 'now')`); err != nil {
		t.Fatal(err)
	}
	var eventID int
	if err := db.QueryRow(`SELECT id FROM deployment_events WHERE task_id='task-1'`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if eventID != 1 {
		t.Fatalf("event id=%d want 1", eventID)
	}
	if _, err := db.Exec(`INSERT INTO deployment_task_values (task_id, value_key, value_text) VALUES ('task-1', 'archive', 'aurora.tgz')`); err != nil {
		t.Fatal(err)
	}
	assertExecFails(t, db, `INSERT INTO deployment_task_values (task_id, value_key, value_text) VALUES ('task-1', 'archive', 'duplicate')`)
	if err := migrateAssetSchema(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployment_tasks WHERE id='task-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("task count=%d want 1 after second migration", count)
	}

	if _, err := db.Exec(`DELETE FROM deployment_tasks WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"deployment_steps", "deployment_events", "deployment_task_values"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count=%d want 0 after task deletion", table, count)
		}
	}

	insertTask(t, "task-3", "succeeded")
	if _, err := db.Exec(`DELETE FROM asset_servers WHERE id='server-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployment_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deployment task count=%d want 0 after server deletion", count)
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
