package aiops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrIncidentNotFound        = errors.New("incident not found")
	ErrIncidentAlreadyExists   = errors.New("incident already exists")
	ErrStateTransitionConflict = errors.New("incident state transition conflict")
)

// IncidentFilter holds optional filter criteria for listing incidents.
// A zero-value filter returns all incidents.
type IncidentFilter struct {
	Status    Status
	Namespace string
}

// IncidentRepository defines the persistence operations for incidents.
type IncidentRepository interface {
	Create(ctx context.Context, inc Incident) error
	Get(ctx context.Context, id string) (Incident, error)
	List(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	UpdateStatus(ctx context.Context, id string, status Status, updatedAt time.Time) error
	TransitionStatus(ctx context.Context, id string, from, to Status, updatedAt time.Time) error
}

type sqlIncidentRepository struct {
	db *sql.DB
}

// NewRepository returns an IncidentRepository backed by the given database.
func NewRepository(db *sql.DB) IncidentRepository {
	return &sqlIncidentRepository{db: db}
}

func (r *sqlIncidentRepository) Create(ctx context.Context, inc Incident) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO incidents (id, summary, severity, status, namespace, resource_kind, resource_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.Summary, string(inc.Severity), string(inc.Status), inc.Namespace,
		inc.ResourceKind, inc.ResourceName,
		inc.CreatedAt.UTC().Format(time.RFC3339Nano),
		inc.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrIncidentAlreadyExists
		}
		return fmt.Errorf("insert incident: %w", err)
	}
	return nil
}

func (r *sqlIncidentRepository) Get(ctx context.Context, id string) (Incident, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, summary, severity, status, namespace, resource_kind, resource_name, created_at, updated_at
		FROM incidents WHERE id = ?`, id)
	return r.scanIncident(row)
}

func (r *sqlIncidentRepository) List(ctx context.Context, filter IncidentFilter) ([]Incident, error) {
	query := `
		SELECT id, summary, severity, status, namespace, resource_kind, resource_name, created_at, updated_at
		FROM incidents`
	var args []any
	var conds []string

	if filter.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(filter.Status))
	}
	if filter.Namespace != "" {
		conds = append(conds, "namespace = ?")
		args = append(args, filter.Namespace)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY updated_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer rows.Close()

	var result []Incident
	for rows.Next() {
		var id, summary, severity, status, namespace, resourceKind, resourceName, createdAtStr, updatedAtStr string
		if err := rows.Scan(&id, &summary, &severity, &status, &namespace, &resourceKind, &resourceName, &createdAtStr, &updatedAtStr); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		result = append(result, Incident{
			ID:           id,
			Summary:      summary,
			Severity:     Severity(severity),
			Status:       Status(status),
			Namespace:    namespace,
			ResourceKind: resourceKind,
			ResourceName: resourceName,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return result, nil
}

func (r *sqlIncidentRepository) UpdateStatus(ctx context.Context, id string, status Status, updatedAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE incidents SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), updatedAt.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("update incident status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrIncidentNotFound
	}
	return nil
}

func (r *sqlIncidentRepository) TransitionStatus(ctx context.Context, id string, from, to Status, updatedAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE incidents SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		string(to), updatedAt.UTC().Format(time.RFC3339Nano), id, string(from),
	)
	if err != nil {
		return fmt.Errorf("transition incident status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 1 {
		return nil
	}
	// No row matched: distinguish a missing incident from a status race.
	if _, err := r.Get(ctx, id); errors.Is(err, ErrIncidentNotFound) {
		return ErrIncidentNotFound
	} else if err != nil {
		return err
	}
	return ErrStateTransitionConflict
}

func (r *sqlIncidentRepository) scanIncident(row *sql.Row) (Incident, error) {
	var id, summary, severity, status, namespace, resourceKind, resourceName, createdAtStr, updatedAtStr string
	if err := row.Scan(&id, &summary, &severity, &status, &namespace, &resourceKind, &resourceName, &createdAtStr, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Incident{}, ErrIncidentNotFound
		}
		return Incident{}, fmt.Errorf("scan incident: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return Incident{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return Incident{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return Incident{
		ID:           id,
		Summary:      summary,
		Severity:     Severity(severity),
		Status:       Status(status),
		Namespace:    namespace,
		ResourceKind: resourceKind,
		ResourceName: resourceName,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}
