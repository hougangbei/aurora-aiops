package remediation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSnapshotNotFound is returned when no snapshot matches the request.
var ErrSnapshotNotFound = errors.New("snapshot not found")

// SnapshotStore persists resource snapshots so approved mutations can be
// rolled back manually.
type SnapshotStore interface {
	Save(ctx context.Context, snapshot Snapshot) error
	Get(ctx context.Context, id string) (Snapshot, error)
	ListByIncident(ctx context.Context, incidentID string) ([]Snapshot, error)
	HasExecuted(ctx context.Context, incidentID, actionKind, resourceName string) (bool, error)
	DeleteByIncident(ctx context.Context, incidentID string) error
}

type sqlSnapshotStore struct {
	db *sql.DB
}

// NewSnapshotStore returns a SnapshotStore backed by db.
func NewSnapshotStore(db *sql.DB) SnapshotStore {
	return &sqlSnapshotStore{db: db}
}

func (s *sqlSnapshotStore) Save(ctx context.Context, snapshot Snapshot) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO remediation_snapshots (id, incident_id, action, api_version, kind, namespace, name, uid, resource_version, yaml, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID, snapshot.IncidentID, snapshot.Action, snapshot.APIVersion, snapshot.Kind,
		snapshot.Namespace, snapshot.Name, snapshot.UID, snapshot.ResourceVersion, snapshot.YAML,
		snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *sqlSnapshotStore) Get(ctx context.Context, id string) (Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, incident_id, action, api_version, kind, namespace, name, uid, resource_version, yaml, created_at
		FROM remediation_snapshots WHERE id = ?`, id)
	var snapshot Snapshot
	var createdAt string
	if err := row.Scan(&snapshot.ID, &snapshot.IncidentID, &snapshot.Action, &snapshot.APIVersion,
		&snapshot.Kind, &snapshot.Namespace, &snapshot.Name, &snapshot.UID, &snapshot.ResourceVersion,
		&snapshot.YAML, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, ErrSnapshotNotFound
		}
		return Snapshot{}, fmt.Errorf("scan snapshot: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot time: %w", err)
	}
	snapshot.CreatedAt = parsed
	return snapshot, nil
}

func (s *sqlSnapshotStore) ListByIncident(ctx context.Context, incidentID string) ([]Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id, action, api_version, kind, namespace, name, uid, resource_version, yaml, created_at
		FROM remediation_snapshots WHERE incident_id = ? ORDER BY created_at ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("query snapshots: %w", err)
	}
	defer rows.Close()
	var snapshots []Snapshot
	for rows.Next() {
		var snapshot Snapshot
		var createdAt string
		if err := rows.Scan(&snapshot.ID, &snapshot.IncidentID, &snapshot.Action, &snapshot.APIVersion,
			&snapshot.Kind, &snapshot.Namespace, &snapshot.Name, &snapshot.UID, &snapshot.ResourceVersion,
			&snapshot.YAML, &createdAt); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		if snapshot.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("parse snapshot time: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshots: %w", err)
	}
	return snapshots, nil
}

func (s *sqlSnapshotStore) HasExecuted(ctx context.Context, incidentID, actionKind, resourceName string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM remediation_snapshots WHERE incident_id = ? AND action = ? AND name = ? LIMIT 1`,
		incidentID, actionKind, resourceName).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check executed snapshot: %w", err)
	}
	return true, nil
}

func (s *sqlSnapshotStore) DeleteByIncident(ctx context.Context, incidentID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM remediation_snapshots WHERE incident_id = ?`, incidentID); err != nil {
		return fmt.Errorf("delete incident snapshots: %w", err)
	}
	return nil
}
