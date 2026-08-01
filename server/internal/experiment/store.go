package experiment

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RunRepository persists experiment runs grouped by strategy.
type RunRepository interface {
	Add(ctx context.Context, run Run) (Run, error)
	ListByGroup(ctx context.Context, group Group) ([]Run, error)
}

type sqlRunRepository struct {
	db *sql.DB
}

// NewRunRepository returns a RunRepository backed by db.
func NewRunRepository(db *sql.DB) RunRepository {
	return &sqlRunRepository{db: db}
}

func (r *sqlRunRepository) Add(ctx context.Context, run Run) (Run, error) {
	if run.ID == "" {
		return Run{}, fmt.Errorf("experiment run id is empty")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO experiment_runs (id, group_name, seed, scenario, expected_root_cause, top1_correct, top3_contains, mttd_seconds, evidence_completeness, high_risk_intercepted, tokens_used, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, string(run.Group), run.Seed, run.Scenario, run.ExpectedRootCause,
		boolToInt(run.Top1Correct), boolToInt(run.Top3Contains),
		run.MTTDSeconds, run.EvidenceCompleteness,
		boolToInt(run.HighRiskIntercepted), run.TokensUsed,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return Run{}, fmt.Errorf("insert experiment run: %w", err)
	}
	return run, nil
}

func (r *sqlRunRepository) ListByGroup(ctx context.Context, group Group) ([]Run, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, group_name, seed, scenario, expected_root_cause, top1_correct, top3_contains, mttd_seconds, evidence_completeness, high_risk_intercepted, tokens_used
		FROM experiment_runs WHERE group_name = ? ORDER BY id ASC`, string(group))
	if err != nil {
		return nil, fmt.Errorf("query experiment runs: %w", err)
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		var run Run
		var top1, top3, intercept int
		if err := rows.Scan(&run.ID, &run.Group, &run.Seed, &run.Scenario, &run.ExpectedRootCause,
			&top1, &top3, &run.MTTDSeconds, &run.EvidenceCompleteness, &intercept, &run.TokensUsed); err != nil {
			return nil, fmt.Errorf("scan experiment run: %w", err)
		}
		run.Top1Correct = top1 == 1
		run.Top3Contains = top3 == 1
		run.HighRiskIntercepted = intercept == 1
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate experiment runs: %w", err)
	}
	return runs, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
