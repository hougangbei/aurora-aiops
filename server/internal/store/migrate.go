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

	return nil
}
