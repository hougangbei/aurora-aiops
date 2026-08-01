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

	return nil
}
