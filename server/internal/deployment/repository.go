package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
)

const repositoryTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

type Repository struct {
	db  *sql.DB
	now func() time.Time
}

func NewRepository(db *sql.DB, now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{db: db, now: now}
}

func (r *Repository) CreateTask(ctx context.Context, task Task, sealed SealedSecret, definitions []StepDefinition) (Task, error) {
	if err := validateNewTask(task, sealed, definitions); err != nil {
		return Task{}, err
	}
	task.Status = TaskQueued
	task.CreatedAt = r.now()
	task.UpdatedAt = task.CreatedAt
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("create task connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return Task{}, fmt.Errorf("create task begin immediate: %w", err)
	}
	defer conn.ExecContext(ctx, `ROLLBACK`)
	_, err = conn.ExecContext(ctx, `INSERT INTO deployment_tasks (id, server_id, project_id, version, action, status, actor, retry_of, config_nonce, config_ciphertext, config_key_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`, task.ID, task.ServerID, task.ProjectID, task.Version, task.Action, task.Status, task.Actor, task.RetryOf, sealed.Nonce, sealed.Ciphertext, sealed.KeyVersion, formatTime(task.CreatedAt), formatTime(task.UpdatedAt))
	if err != nil {
		if isActiveTaskConflict(err) {
			return Task{}, ErrActiveTask
		}
		return Task{}, fmt.Errorf("create task insert: %w", err)
	}
	for ordinal, definition := range definitions {
		if _, err := conn.ExecContext(ctx, `INSERT INTO deployment_steps (task_id, step_id, label, ordinal, percent, status) VALUES (?, ?, ?, ?, ?, ?)`, task.ID, definition.ID, definition.Label, ordinal, definition.Percent, StepPending); err != nil {
			return Task{}, fmt.Errorf("create task step: %w", err)
		}
		for _, key := range definition.ValueKeys {
			if _, err := conn.ExecContext(ctx, `INSERT INTO deployment_task_values (task_id, value_key, value_text) VALUES (?, ?, '')`, task.ID, key); err != nil {
				return Task{}, fmt.Errorf("create task value key: %w", err)
			}
		}
	}
	if err := appendEvent(ctx, conn, task.ID, EventInput{Type: "queued"}, task.CreatedAt); err != nil {
		return Task{}, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return Task{}, fmt.Errorf("create task commit: %w", err)
	}
	return task, nil
}

func (r *Repository) GetTask(ctx context.Context, id string) (Task, error) {
	task, err := queryTask(ctx, r.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}
func (r *Repository) ListServerTasks(ctx context.Context, serverID string) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, taskSelect+` WHERE server_id=? ORDER BY created_at,id`, serverID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}
func (r *Repository) ListSteps(ctx context.Context, taskID string) ([]Step, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT task_id,step_id,label,ordinal,percent,status,started_at,finished_at,error_message FROM deployment_steps WHERE task_id=? ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		s, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) OpenTaskConfiguration(ctx context.Context, id string, cipher SecretCipher) (json.RawMessage, error) {
	if cipher == nil {
		return nil, ErrEncryptionUnavailable
	}
	var sealed SealedSecret
	err := r.db.QueryRowContext(ctx, `SELECT config_nonce,config_ciphertext,config_key_version FROM deployment_tasks WHERE id=?`, id).Scan(&sealed.Nonce, &sealed.Ciphertext, &sealed.KeyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read task configuration: %w", err)
	}
	value, err := cipher.Open(TaskConfigScope, id, sealed)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(value), nil
}

func (r *Repository) PutValue(ctx context.Context, taskID, key, value string) error {
	if key == "" {
		return ErrInvalidInput
	}
	result, err := r.db.ExecContext(ctx, `UPDATE deployment_task_values SET value_text=? WHERE task_id=? AND value_key=?`, value, taskID, key)
	if err != nil {
		return fmt.Errorf("put deployment value: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrInvalidInput
	}
	return nil
}
func (r *Repository) GetValue(ctx context.Context, taskID, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value_text FROM deployment_task_values WHERE task_id=? AND value_key=?`, taskID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get deployment value: %w", err)
	}
	return value, true, nil
}

func (r *Repository) ClaimNext(ctx context.Context, owner string, lease time.Duration) (Task, bool, error) {
	if owner == "" || lease <= 0 {
		return Task{}, false, ErrInvalidInput
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return Task{}, false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return Task{}, false, err
	}
	defer conn.ExecContext(ctx, `ROLLBACK`)
	var id string
	err = conn.QueryRowContext(ctx, `SELECT id FROM deployment_tasks WHERE status=? ORDER BY created_at,id LIMIT 1`, TaskQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	now := r.now()
	res, err := conn.ExecContext(ctx, `UPDATE deployment_tasks SET status=?,lease_owner=?,lease_expires_at=?,started_at=CASE WHEN started_at='' THEN ? ELSE started_at END,updated_at=? WHERE id=? AND status=?`, TaskRunning, owner, formatTime(now.Add(lease)), formatTime(now), formatTime(now), id, TaskQueued)
	if err != nil {
		return Task{}, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Task{}, false, ErrInvalidTransition
	}
	if err := appendEvent(ctx, conn, id, EventInput{Type: "running"}, now); err != nil {
		return Task{}, false, err
	}
	task, err := queryTask(ctx, conn, id)
	if err != nil {
		return Task{}, false, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return Task{}, false, err
	}
	return task, true, nil
}

func (r *Repository) RenewLease(ctx context.Context, id, owner string, lease time.Duration) error {
	if owner == "" || lease <= 0 {
		return ErrInvalidInput
	}
	now := r.now()
	res, err := r.db.ExecContext(ctx, `UPDATE deployment_tasks SET lease_expires_at=?,updated_at=? WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?`, formatTime(now.Add(lease)), formatTime(now), id, TaskRunning, owner, formatTime(now))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}
func (r *Repository) StartStep(ctx context.Context, taskID, stepID, label string, event EventInput) error {
	return r.stepTransition(ctx, taskID, stepID, func(tx *sql.Tx, now time.Time, task Task) error {
		var earlier int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployment_steps WHERE task_id=? AND ordinal < (SELECT ordinal FROM deployment_steps WHERE task_id=? AND step_id=?) AND status NOT IN (?, ?)`, taskID, taskID, stepID, StepSucceeded, StepSkipped).Scan(&earlier); err != nil {
			return err
		}
		if earlier != 0 {
			return ErrInvalidTransition
		}
		res, err := tx.ExecContext(ctx, `UPDATE deployment_steps SET status=?,started_at=? WHERE task_id=? AND step_id=? AND status=? AND EXISTS (SELECT 1 FROM deployment_tasks WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?)`, StepRunning, formatTime(now), taskID, stepID, StepPending, taskID, TaskRunning, event.Owner, formatTime(now))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		_, err = tx.ExecContext(ctx, `UPDATE deployment_tasks SET current_step_id=?,current_step_label=?,updated_at=? WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?`, stepID, label, formatTime(now), taskID, TaskRunning, event.Owner, formatTime(now))
		return err
	}, event)
}
func (r *Repository) CompleteStep(ctx context.Context, taskID, stepID string, skipped bool, percent int, event EventInput) error {
	return r.stepTransition(ctx, taskID, stepID, func(tx *sql.Tx, now time.Time, task Task) error {
		if percent <= task.Percent || percent > 100 {
			return ErrInvalidTransition
		}
		status := StepSucceeded
		if skipped {
			status = StepSkipped
		}
		res, err := tx.ExecContext(ctx, `UPDATE deployment_steps SET status=?,finished_at=? WHERE task_id=? AND step_id=? AND status=? AND percent=? AND EXISTS (SELECT 1 FROM deployment_tasks WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?)`, status, formatTime(now), taskID, stepID, StepRunning, percent, taskID, TaskRunning, event.Owner, formatTime(now))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		res, err = tx.ExecContext(ctx, `UPDATE deployment_tasks SET percent=?,updated_at=? WHERE id=? AND status=? AND percent<? AND lease_owner=? AND lease_expires_at>?`, percent, formatTime(now), taskID, TaskRunning, percent, event.Owner, formatTime(now))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		return nil
	}, event)
}
func (r *Repository) FailStep(ctx context.Context, taskID, stepID, message string, event EventInput) error {
	return r.stepTransition(ctx, taskID, stepID, func(tx *sql.Tx, now time.Time, task Task) error {
		res, err := tx.ExecContext(ctx, `UPDATE deployment_steps SET status=?,finished_at=?,error_message=? WHERE task_id=? AND step_id=? AND status=? AND EXISTS (SELECT 1 FROM deployment_tasks WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?)`, StepFailed, formatTime(now), message, taskID, stepID, StepRunning, taskID, TaskRunning, event.Owner, formatTime(now))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		return nil
	}, event)
}
func (r *Repository) RequestCancel(ctx context.Context, id string, event EventInput) error {
	return r.taskTransition(ctx, id, TaskRunning, func(tx *sql.Tx, now time.Time) error {
		res, err := tx.ExecContext(ctx, `UPDATE deployment_tasks SET cancel_requested=1,updated_at=? WHERE id=? AND status=? AND cancel_requested=0`, formatTime(now), id, TaskRunning)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		return nil
	}, event)
}
func (r *Repository) Finish(ctx context.Context, id string, status TaskStatus, code, message string, event EventInput) error {
	if !terminal(status) {
		return ErrInvalidInput
	}
	return r.fencedTaskTransition(ctx, id, event, func(tx *sql.Tx, now time.Time, task Task) error {
		if status == TaskSucceeded {
			if task.Percent != 100 {
				return ErrInvalidTransition
			}
			var incomplete int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployment_steps WHERE task_id=? AND status NOT IN (?, ?)`, id, StepSucceeded, StepSkipped).Scan(&incomplete); err != nil {
				return err
			}
			if incomplete != 0 {
				return ErrInvalidTransition
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE deployment_tasks SET status=?,error_code=?,error_message=?,finished_at=?,lease_owner='',lease_expires_at='',updated_at=? WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?`, status, code, message, formatTime(now), formatTime(now), id, TaskRunning, event.Owner, formatTime(now))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalidTransition
		}
		return nil
	})
}

func (r *Repository) RecoverExpired(ctx context.Context) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := r.now()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM deployment_tasks WHERE status=? AND lease_expires_at<>'' AND lease_expires_at<?`, TaskRunning, formatTime(now))
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()
	var recovered int64
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `UPDATE deployment_tasks SET status=?,lease_owner='',lease_expires_at='',current_step_id='',current_step_label='',updated_at=? WHERE id=? AND status=? AND lease_expires_at<>'' AND lease_expires_at<?`, TaskQueued, formatTime(now), id, TaskRunning, formatTime(now))
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			if _, err := tx.ExecContext(ctx, `UPDATE deployment_steps SET status=?,started_at='',finished_at='',error_message='' WHERE task_id=? AND status=?`, StepPending, id, StepRunning); err != nil {
				return 0, err
			}
			if err := appendEvent(ctx, tx, id, EventInput{Type: "requeued"}, now); err != nil {
				return 0, err
			}
			recovered++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return recovered, nil
}

func (r *Repository) Retry(ctx context.Context, id, newID, actor string, _ []StepDefinition) (Task, error) {
	if newID == "" || actor == "" {
		return Task{}, ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	old, err := queryTask(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if !terminal(old.Status) {
		return Task{}, ErrNotRetryable
	}
	now := r.now()
	_, err = tx.ExecContext(ctx, `INSERT INTO deployment_tasks (id,server_id,project_id,version,action,status,actor,retry_of,config_nonce,config_ciphertext,config_key_version,created_at,updated_at) SELECT ?,server_id,project_id,version,action,?, ?,id,config_nonce,config_ciphertext,config_key_version,?,? FROM deployment_tasks WHERE id=?`, newID, TaskQueued, actor, formatTime(now), formatTime(now), id)
	if err != nil {
		if isActiveTaskConflict(err) {
			return Task{}, ErrActiveTask
		}
		return Task{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT step_id,label,ordinal,percent FROM deployment_steps WHERE task_id=? ORDER BY ordinal`, id)
	if err != nil {
		return Task{}, err
	}
	for rows.Next() {
		var sid, label string
		var ordinal, percent int
		if err := rows.Scan(&sid, &label, &ordinal, &percent); err != nil {
			return Task{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deployment_steps(task_id,step_id,label,ordinal,percent,status) VALUES(?,?,?,?,?,?)`, newID, sid, label, ordinal, percent, StepPending); err != nil {
			return Task{}, err
		}
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `INSERT INTO deployment_task_values(task_id,value_key,value_text) SELECT ?,value_key,'' FROM deployment_task_values WHERE task_id=?`, newID, id); err != nil {
		return Task{}, err
	}
	if err := appendEvent(ctx, tx, newID, EventInput{Type: "queued"}, now); err != nil {
		return Task{}, err
	}
	task, err := queryTask(ctx, tx, newID)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (r *Repository) taskTransition(ctx context.Context, id string, expected TaskStatus, change func(*sql.Tx, time.Time) error, event EventInput) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureStatus(ctx, tx, id, expected); err != nil {
		return err
	}
	now := r.now()
	if err := change(tx, now); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, event, now); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) stepTransition(ctx context.Context, id, stepID string, change func(*sql.Tx, time.Time, Task) error, event EventInput) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	task, err := queryTask(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if task.Status != TaskRunning {
		return ErrInvalidTransition
	}
	now := r.now()
	if err := ensureLease(ctx, tx, id, event.Owner, now); err != nil {
		return err
	}
	if err := change(tx, now, task); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, event, now); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) fencedTaskTransition(ctx context.Context, id string, event EventInput, change func(*sql.Tx, time.Time, Task) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	task, err := queryTask(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if task.Status != TaskRunning {
		return ErrInvalidTransition
	}
	now := r.now()
	if err := ensureLease(ctx, tx, id, event.Owner, now); err != nil {
		return err
	}
	if err := change(tx, now, task); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, id, event, now); err != nil {
		return err
	}
	return tx.Commit()
}
func ensureLease(ctx context.Context, tx *sql.Tx, id, owner string, now time.Time) error {
	if owner == "" {
		return ErrLeaseLost
	}
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM deployment_tasks WHERE id=? AND status=? AND lease_owner=? AND lease_expires_at>?`, id, TaskRunning, owner, formatTime(now)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	return nil
}
func ensureStatus(ctx context.Context, tx *sql.Tx, id string, expected TaskStatus) error {
	var status TaskStatus
	err := tx.QueryRowContext(ctx, `SELECT status FROM deployment_tasks WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != expected {
		return ErrInvalidTransition
	}
	return nil
}

const taskSelect = `SELECT id,server_id,project_id,version,action,status,actor,COALESCE(retry_of,''),current_step_id,current_step_label,percent,cancel_requested,error_code,error_message,created_at,updated_at,started_at,finished_at FROM deployment_tasks`

type scanner interface{ Scan(...any) error }

func queryTask(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Task, error) {
	return scanTask(q.QueryRowContext(ctx, taskSelect+` WHERE id=?`, id))
}
func scanTask(s scanner) (Task, error) {
	var t Task
	var cancel int
	var created, updated, started, finished string
	err := s.Scan(&t.ID, &t.ServerID, &t.ProjectID, &t.Version, &t.Action, &t.Status, &t.Actor, &t.RetryOf, &t.CurrentStepID, &t.CurrentStepLabel, &t.Percent, &cancel, &t.ErrorCode, &t.ErrorMessage, &created, &updated, &started, &finished)
	if err != nil {
		return Task{}, err
	}
	var parseErr error
	t.CancelRequested = cancel != 0
	t.CreatedAt, parseErr = parseTime(created)
	if parseErr != nil {
		return Task{}, parseErr
	}
	t.UpdatedAt, parseErr = parseTime(updated)
	if parseErr != nil {
		return Task{}, parseErr
	}
	t.StartedAt, parseErr = parseOptionalTime(started)
	if parseErr != nil {
		return Task{}, parseErr
	}
	t.FinishedAt, parseErr = parseOptionalTime(finished)
	return t, parseErr
}
func scanTasks(rows *sql.Rows) ([]Task, error) {
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func scanStep(s scanner) (Step, error) {
	var step Step
	var started, finished string
	err := s.Scan(&step.TaskID, &step.ID, &step.Label, &step.Ordinal, &step.Percent, &step.Status, &started, &finished, &step.ErrorMessage)
	if err != nil {
		return Step{}, err
	}
	step.StartedAt, err = parseOptionalTime(started)
	if err != nil {
		return Step{}, err
	}
	step.FinishedAt, err = parseOptionalTime(finished)
	return step, err
}
func appendEvent(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id string, event EventInput, now time.Time) error {
	if event.Type == "" {
		return ErrInvalidInput
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	if !json.Valid(payload) {
		return ErrInvalidInput
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO deployment_events(task_id,event_type,payload,created_at) VALUES(?,?,?,?)`, id, event.Type, string(payload), formatTime(now))
	return err
}
func validateNewTask(t Task, sealed SealedSecret, steps []StepDefinition) error {
	if t.ID == "" || t.ServerID == "" || t.ProjectID == "" || t.Version == "" || t.Actor == "" || (t.Action != TaskActionInstall && t.Action != TaskActionAdopt) || sealed.KeyVersion != secretKeyVersion || len(sealed.Nonce) == 0 || len(sealed.Ciphertext) == 0 || len(steps) == 0 {
		return ErrInvalidInput
	}
	last := 0
	keys := map[string]bool{}
	for _, s := range steps {
		if s.ID == "" || s.Label == "" || s.Percent <= last || s.Percent > 100 {
			return ErrInvalidInput
		}
		last = s.Percent
		for _, key := range s.ValueKeys {
			if key == "" || keys[key] {
				return ErrInvalidInput
			}
			keys[key] = true
		}
	}
	if last != 100 {
		return ErrInvalidInput
	}
	return nil
}
func terminal(s TaskStatus) bool { return s == TaskSucceeded || s == TaskFailed || s == TaskCancelled }
func isActiveTaskConflict(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067
}
func formatTime(t time.Time) string         { return t.UTC().Format(repositoryTimeFormat) }
func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
func parseOptionalTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	v, err := parseTime(s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
