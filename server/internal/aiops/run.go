package aiops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// RunStatus tracks the lifecycle of one agent run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusSkipped   RunStatus = "skipped"
)

var (
	ErrRunNotFound         = errors.New("agent run not found")
	ErrWorkflowLocked      = errors.New("workflow already running for incident")
	ErrReanalyzeNotAllowed = errors.New("reanalyze not allowed for current incident status")
)

// AgentRun is one execution of a diagnostic role for an incident.
type AgentRun struct {
	ID               string    `json:"id"`
	IncidentID       string    `json:"incidentId"`
	Role             string    `json:"role"`
	Attempt          int       `json:"attempt"`
	Status           RunStatus `json:"status"`
	Summary          string    `json:"summary"`
	Output           string    `json:"output"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	TotalTokens      int       `json:"totalTokens"`
	Error            string    `json:"error"`
	StartedAt        time.Time `json:"startedAt"`
	CompletedAt      time.Time `json:"completedAt"`
}

// RunRepository persists agent runs. CompleteRun writes the finished run and
// the incident's next status in a single transaction, so a completed run and
// the corresponding status advance are atomic: on restart the workflow can
// trust that a succeeded run means its role is finished.
type RunRepository interface {
	CreateRun(ctx context.Context, run AgentRun) error
	CompleteRun(ctx context.Context, run AgentRun, incidentStatus Status, now time.Time) error
	ListRuns(ctx context.Context, incidentID string) ([]AgentRun, error)
	NextAttempt(ctx context.Context, incidentID, role string) (int, error)
}

type sqlRunRepository struct {
	db *sql.DB
}

// NewRunRepository returns a RunRepository backed by db.
func NewRunRepository(db *sql.DB) RunRepository {
	return &sqlRunRepository{db: db}
}

func (r *sqlRunRepository) CreateRun(ctx context.Context, run AgentRun) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_runs (id, incident_id, role, attempt, status, summary, output, model, prompt_tokens, completion_tokens, total_tokens, error, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.IncidentID, run.Role, run.Attempt, string(run.Status),
		run.Summary, run.Output, run.Model,
		run.PromptTokens, run.CompletionTokens, run.TotalTokens,
		run.Error, run.StartedAt.UTC().Format(time.RFC3339Nano),
		run.CompletedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert agent run: %w", err)
	}
	return nil
}

// CompleteRun finishes the run and, when incidentStatus is non-empty, validates
// and applies the incident transition in the same transaction. Runs that do not
// advance the incident (incidentStatus == "") need no transaction and use a
// single UPDATE.
func (r *sqlRunRepository) CompleteRun(ctx context.Context, run AgentRun, incidentStatus Status, now time.Time) error {
	if incidentStatus == "" {
		return r.finishRun(ctx, run, now)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run tx: %w", err)
	}
	defer tx.Rollback()

	if err := r.updateRun(ctx, tx, run, now); err != nil {
		return err
	}

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM incidents WHERE id = ?`, run.IncidentID).Scan(&current); err != nil {
		return fmt.Errorf("load incident status: %w", err)
	}
	if !CanTransition(Status(current), incidentStatus) {
		return ErrInvalidStateTransition
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE incidents SET status = ?, updated_at = ? WHERE id = ?`,
		string(incidentStatus), now.UTC().Format(time.RFC3339Nano), run.IncidentID,
	); err != nil {
		return fmt.Errorf("advance incident status: %w", err)
	}

	return tx.Commit()
}

// finishRun writes the run completion without touching the incident.
func (r *sqlRunRepository) finishRun(ctx context.Context, run AgentRun, now time.Time) error {
	if err := r.updateRun(ctx, r.db, run, now); err != nil {
		return err
	}
	return nil
}

// updateRun executes the agent_runs completion UPDATE on the given querier.
type runExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *sqlRunRepository) updateRun(ctx context.Context, execer runExecer, run AgentRun, now time.Time) error {
	_, err := execer.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = ?, summary = ?, output = ?, model = ?, prompt_tokens = ?, completion_tokens = ?, total_tokens = ?, error = ?, completed_at = ?
		WHERE id = ?`,
		string(run.Status), run.Summary, run.Output, run.Model,
		run.PromptTokens, run.CompletionTokens, run.TotalTokens,
		run.Error, now.UTC().Format(time.RFC3339Nano), run.ID,
	)
	if err != nil {
		return fmt.Errorf("complete agent run: %w", err)
	}
	return nil
}

func (r *sqlRunRepository) ListRuns(ctx context.Context, incidentID string) ([]AgentRun, error) {
	// Order by insertion rowid: started_at is RFC3339Nano text whose lexical
	// order is not monotonic (trailing zeros are stripped), so a timestamp sort
	// can reorder runs that were created sequentially.
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, incident_id, role, attempt, status, summary, output, model, prompt_tokens, completion_tokens, total_tokens, error, started_at, completed_at
		FROM agent_runs WHERE incident_id = ? ORDER BY rowid ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("query agent runs: %w", err)
	}
	defer rows.Close()

	var runs []AgentRun
	for rows.Next() {
		var run AgentRun
		var status, startedAtStr, completedAtStr string
		if err := rows.Scan(&run.ID, &run.IncidentID, &run.Role, &run.Attempt, &status,
			&run.Summary, &run.Output, &run.Model,
			&run.PromptTokens, &run.CompletionTokens, &run.TotalTokens,
			&run.Error, &startedAtStr, &completedAtStr); err != nil {
			return nil, fmt.Errorf("scan agent run: %w", err)
		}
		run.Status = RunStatus(status)
		if run.StartedAt, err = time.Parse(time.RFC3339Nano, startedAtStr); err != nil {
			return nil, fmt.Errorf("parse run started_at: %w", err)
		}
		if completedAtStr != "" {
			if run.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAtStr); err != nil {
				return nil, fmt.Errorf("parse run completed_at: %w", err)
			}
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent runs: %w", err)
	}
	return runs, nil
}

func (r *sqlRunRepository) NextAttempt(ctx context.Context, incidentID, role string) (int, error) {
	var maxAttempt int
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(attempt), 0) FROM agent_runs WHERE incident_id = ? AND role = ?`,
		incidentID, role).Scan(&maxAttempt)
	if err != nil {
		return 0, fmt.Errorf("next attempt: %w", err)
	}
	return maxAttempt + 1, nil
}

func generateRunID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "run-" + hex.EncodeToString(b), nil
}
