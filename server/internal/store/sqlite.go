package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}

	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}
