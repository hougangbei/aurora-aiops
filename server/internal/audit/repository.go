package audit

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository persists append-only audit records. Only Append and List are
// exposed: there is intentionally no Update or Delete, so the application layer
// cannot rewrite history.
type Repository interface {
	Append(ctx context.Context, record Record) (Record, error)
	List(ctx context.Context) ([]Record, error)
}

type sqlRepository struct {
	db *sql.DB
}

// NewRepository returns an audit Repository backed by db.
func NewRepository(db *sql.DB) Repository {
	return &sqlRepository{db: db}
}

// Append redacts the payload, hashes it against the previous record, and
// inserts the new row. The hash is computed after the auto-increment id is
// assigned, so the canonical record used for hashing carries its final id.
func (r *sqlRepository) Append(ctx context.Context, record Record) (Record, error) {
	record.Payload = RedactPayload(record.Payload)
	record.Timestamp = record.Timestamp.UTC()
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("begin audit tx: %w", err)
	}
	defer tx.Rollback()

	// Read the previous hash before inserting, so it refers to the last
	// committed record rather than the row about to be created.
	previousHash, err := r.latestHash(ctx, tx)
	if err != nil {
		return Record{}, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO audit_records (actor, action, target, result, payload, timestamp, hash)
		VALUES (?, ?, ?, ?, ?, ?, '')`,
		record.Actor, record.Action, record.Target, record.Result,
		record.Payload, record.Timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Record{}, fmt.Errorf("insert audit record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Record{}, fmt.Errorf("audit record id: %w", err)
	}
	record.ID = id
	record.Hash = Hash(previousHash, record)

	if _, err := tx.ExecContext(ctx, `UPDATE audit_records SET hash = ? WHERE id = ?`, record.Hash, id); err != nil {
		return Record{}, fmt.Errorf("seal audit record hash: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("commit audit tx: %w", err)
	}
	return record, nil
}

func (r *sqlRepository) latestHash(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}) (string, error) {
	var hash string
	err := q.QueryRowContext(ctx, `SELECT hash FROM audit_records ORDER BY id DESC LIMIT 1`).Scan(&hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("latest audit hash: %w", err)
	}
	return hash, nil
}

func (r *sqlRepository) List(ctx context.Context) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, actor, action, target, result, payload, timestamp, hash
		FROM audit_records ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query audit records: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var record Record
		var timestamp string
		if err := rows.Scan(&record.ID, &record.Actor, &record.Action, &record.Target,
			&record.Result, &record.Payload, &timestamp, &record.Hash); err != nil {
			return nil, fmt.Errorf("scan audit record: %w", err)
		}
		if record.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp); err != nil {
			return nil, fmt.Errorf("parse audit timestamp: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit records: %w", err)
	}
	return records, nil
}
