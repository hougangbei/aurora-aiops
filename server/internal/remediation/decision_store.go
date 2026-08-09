package remediation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/aiops"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
)

// DecisionStore atomically persists a terminal incident decision and its
// mandatory audit-chain record.
type DecisionStore struct {
	db *sql.DB
}

// NewDecisionStore returns a transaction coordinator backed by db.
func NewDecisionStore(db *sql.DB) *DecisionStore {
	return &DecisionStore{db: db}
}

// Decide performs the awaiting_approval compare-and-swap and audit append in
// one transaction. Exactly one concurrent caller can commit a decision.
func (s *DecisionStore) Decide(
	ctx context.Context,
	incidentID string,
	status aiops.Status,
	record audit.Record,
	updatedAt time.Time,
) error {
	if status != aiops.StatusApproved && status != aiops.StatusRejected {
		return aiops.ErrInvalidStateTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remediation decision tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE incidents SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(status), updatedAt.UTC().Format(time.RFC3339Nano),
		incidentID, string(aiops.StatusAwaitingApproval),
	)
	if err != nil {
		return fmt.Errorf("transition incident decision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("decision rows affected: %w", err)
	}
	if rows == 0 {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM incidents WHERE id = ?`, incidentID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return aiops.ErrIncidentNotFound
		}
		if err != nil {
			return fmt.Errorf("check incident after decision conflict: %w", err)
		}
		return aiops.ErrStateTransitionConflict
	}

	if _, err := audit.AppendTx(ctx, tx, record); err != nil {
		return fmt.Errorf("append remediation decision audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remediation decision tx: %w", err)
	}
	return nil
}
