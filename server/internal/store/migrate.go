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

	return nil
}
