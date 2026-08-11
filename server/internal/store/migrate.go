package store

import "database/sql"

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS incidents (
  id TEXT PRIMARY KEY,
  summary TEXT NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
  status TEXT NOT NULL,
  namespace TEXT NOT NULL,
  resource_kind TEXT NOT NULL,
  resource_name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_incidents_status_updated
ON incidents(status, updated_at DESC)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
  digest TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_sessions_user_expires
ON sessions(user_id, expires_at)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS evidence_nodes (
  incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('snapshot','event','log','metric','agent','system')),
  payload TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (incident_id, id)
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_evidence_nodes_incident
ON evidence_nodes(incident_id)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS evidence_edges (
  incident_id TEXT NOT NULL,
  from_id TEXT NOT NULL,
  to_id TEXT NOT NULL,
  relation TEXT NOT NULL CHECK (relation IN ('supports','contradicts')),
  created_at TEXT NOT NULL,
  PRIMARY KEY (incident_id, from_id, to_id),
  FOREIGN KEY (incident_id, from_id) REFERENCES evidence_nodes(incident_id, id) ON DELETE CASCADE,
  FOREIGN KEY (incident_id, to_id) REFERENCES evidence_nodes(incident_id, id) ON DELETE CASCADE
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_evidence_edges_incident
ON evidence_edges(incident_id)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS agent_runs (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  attempt INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
  summary TEXT NOT NULL DEFAULT '',
  output TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL DEFAULT ''
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_agent_runs_incident
ON agent_runs(incident_id, role, attempt)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_locks (
  incident_id TEXT PRIMARY KEY,
  acquired_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS incident_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_incident_events_incident
ON incident_events(incident_id, id)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS audit_records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  result TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '',
  timestamp TEXT NOT NULL,
  hash TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS remediation_snapshots (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  api_version TEXT NOT NULL,
  kind TEXT NOT NULL,
  namespace TEXT NOT NULL,
  name TEXT NOT NULL,
  uid TEXT NOT NULL,
  resource_version TEXT NOT NULL,
  yaml TEXT NOT NULL,
  created_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_remediation_snapshots_incident
ON remediation_snapshots(incident_id, action)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS experiment_runs (
  id TEXT PRIMARY KEY,
  group_name TEXT NOT NULL,
  seed INTEGER NOT NULL,
  scenario TEXT NOT NULL,
  expected_root_cause TEXT NOT NULL,
  top1_correct INTEGER NOT NULL DEFAULT 0,
  top3_contains INTEGER NOT NULL DEFAULT 0,
  mttd_seconds REAL NOT NULL DEFAULT 0,
  evidence_completeness REAL NOT NULL DEFAULT 0,
  high_risk_intercepted INTEGER NOT NULL DEFAULT 0,
  tokens_used INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_experiment_runs_group
ON experiment_runs(group_name)`)
	if err != nil {
		return err
	}

	return migrateAssetSchema(db)
}

func migrateAssetSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS asset_credentials (
  id TEXT PRIMARY KEY,
  auth_type TEXT NOT NULL CHECK (auth_type IN ('password','private_key')),
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS asset_servers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  address TEXT NOT NULL,
  ssh_port INTEGER NOT NULL CHECK (ssh_port BETWEEN 1 AND 65535),
  username TEXT NOT NULL,
  credential_id TEXT NOT NULL REFERENCES asset_credentials(id),
  host_key_fingerprint TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending','online','offline','error')),
  status_message TEXT NOT NULL DEFAULT '',
  os_family TEXT NOT NULL DEFAULT '',
  os_version TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  cpu_cores INTEGER NOT NULL DEFAULT 0,
  memory_bytes INTEGER NOT NULL DEFAULT 0,
  disk_bytes INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT NOT NULL DEFAULT '',
  last_collected_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS asset_snapshots (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  payload TEXT NOT NULL,
  collected_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_asset_snapshots_server_time
ON asset_snapshots(server_id, collected_at DESC)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS asset_software_items (
  snapshot_id TEXT NOT NULL REFERENCES asset_snapshots(id) ON DELETE CASCADE,
  category TEXT NOT NULL,
  name TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (snapshot_id, category, name, architecture)
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS project_installations (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL,
  install_path TEXT NOT NULL DEFAULT '',
  health_summary TEXT NOT NULL DEFAULT '',
  last_task_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(server_id, project_id)
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS deployment_tasks (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  version TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('install','adopt')),
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','cancelled')),
  actor TEXT NOT NULL,
  retry_of TEXT,
  current_step_id TEXT NOT NULL DEFAULT '',
  current_step_label TEXT NOT NULL DEFAULT '',
  percent INTEGER NOT NULL DEFAULT 0 CHECK (percent BETWEEN 0 AND 100),
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  config_nonce BLOB NOT NULL,
  config_ciphertext BLOB NOT NULL,
  config_key_version INTEGER NOT NULL DEFAULT 1,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(retry_of) REFERENCES deployment_tasks(id)
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployment_tasks_one_active_server
ON deployment_tasks(server_id) WHERE status IN ('queued','running')`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_deployment_tasks_status_created
ON deployment_tasks(status, created_at)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS deployment_steps (
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  step_id TEXT NOT NULL,
  label TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  percent INTEGER NOT NULL CHECK (percent BETWEEN 1 AND 100),
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped','cancelled')),
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(task_id, step_id),
  UNIQUE(task_id, ordinal)
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS deployment_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_deployment_events_task_id
ON deployment_events(task_id, id)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS deployment_task_values (
  task_id TEXT NOT NULL REFERENCES deployment_tasks(id) ON DELETE CASCADE,
  value_key TEXT NOT NULL,
  value_text TEXT NOT NULL,
  PRIMARY KEY(task_id, value_key)
)`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS deployment_secrets (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES asset_servers(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL,
  secret_kind TEXT NOT NULL CHECK (secret_kind IN ('kubeconfig')),
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(server_id, project_id, secret_kind)
)`)
	if err != nil {
		return err
	}

	return tx.Commit()
}
