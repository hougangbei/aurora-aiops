package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
)

// TargetProvider builds an execution context for a server. Implementations
// keep credentials private inside the returned context.
type TargetProvider interface {
	ExecutionContext(context.Context, string, string) (assets.Server, ExecutionContext, error)
}

type WorkerOptions struct {
	Owner string
	Lease time.Duration
	Poll  time.Duration
}

// Worker executes one durable deployment at a time. The repository lease is
// the authority that fences stale workers after a process restart.
type Worker struct {
	repo     *Repository
	catalog  *Catalog
	cipher   SecretCipher
	provider TargetProvider
	owner    string
	lease    time.Duration
	poll     time.Duration
}

func NewWorker(repo *Repository, catalog *Catalog, cipher SecretCipher, provider TargetProvider, options ...WorkerOptions) *Worker {
	opts := WorkerOptions{Owner: "deployment-worker", Lease: 2 * time.Minute, Poll: time.Second}
	if len(options) > 0 {
		if options[0].Owner != "" {
			opts.Owner = options[0].Owner
		}
		if options[0].Lease > 0 {
			opts.Lease = options[0].Lease
		}
		if options[0].Poll > 0 {
			opts.Poll = options[0].Poll
		}
	}
	return &Worker{repo: repo, catalog: catalog, cipher: cipher, provider: provider, owner: opts.Owner, lease: opts.Lease, poll: opts.Poll}
}

// RunOnce recovers expired leases, claims at most one queued task, and runs it.
// It is useful for deterministic tests and for embedding in a scheduler.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.repo == nil || w.catalog == nil || w.cipher == nil || w.provider == nil {
		return false, ErrInvalidInput
	}
	if _, err := w.repo.RecoverExpired(ctx); err != nil {
		return false, err
	}
	task, claimed, err := w.repo.ClaimNext(ctx, w.owner, w.lease)
	if err != nil || !claimed {
		return claimed, err
	}
	return true, w.executeClaimed(ctx, task)
}

// Start runs the single worker goroutine until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) error {
	if w == nil {
		return ErrInvalidInput
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		claimed, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if claimed {
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) executeClaimed(ctx context.Context, task Task) error {
	installer, ok := w.catalog.Installer(task.ProjectID)
	if !ok {
		return w.failTask(ctx, task, "UNKNOWN_PROJECT", ErrUnknownProject.Error())
	}
	config, err := w.repo.OpenTaskConfiguration(ctx, task.ID, w.cipher)
	if err != nil {
		return w.failTask(ctx, task, "CONFIG_OPEN_FAILED", "unable to open deployment configuration")
	}
	secrets := scalarSecrets(config)
	logger := NewRedactingLogger(secrets...)
	server, execCtx, err := w.provider.ExecutionContext(ctx, task.ServerID, task.ID)
	if err != nil {
		return w.failTask(ctx, task, "TARGET_UNAVAILABLE", safeMessage(logger, err))
	}
	plan, err := installer.BuildPlan(task, server, config)
	if err != nil {
		return w.failTask(ctx, task, "PLAN_FAILED", safeMessage(logger, err))
	}
	if err := validateWorkerPlan(plan); err != nil {
		return w.failTask(ctx, task, "PLAN_FAILED", err.Error())
	}
	steps, err := w.repo.ListSteps(ctx, task.ID)
	if err != nil {
		return err
	}
	if len(steps) != len(plan) {
		return w.failTask(ctx, task, "PLAN_MISMATCH", "deployment plan changed")
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewLost := make(chan struct{}, 1)
	stopRenew := make(chan struct{})
	go w.renewLease(workCtx, task.ID, stopRenew, renewLost, cancel)
	defer close(stopRenew)

	for index, definition := range plan {
		if steps[index].Status == StepSucceeded || steps[index].Status == StepSkipped {
			continue
		}
		current, err := w.repo.GetTask(ctx, task.ID)
		if err != nil {
			return err
		}
		if current.CancelRequested {
			return w.finishCancelled(ctx, task.ID)
		}
		if err := w.repo.StartStep(ctx, task.ID, definition.ID, definition.Label, EventInput{Type: "step_started", Owner: w.owner}); err != nil {
			return err
		}
		logger.Info("starting step " + definition.ID)
		probeCtx, probeCancel := context.WithTimeout(workCtx, stepTimeout(definition))
		probed, probeErr := callProbe(definition.Probe, probeCtx, execCtx)
		probeCancel()
		if probeErr != nil {
			if errors.Is(probeErr, context.Canceled) && w.cancelRequested(ctx, task.ID) {
				return w.finishCancelled(ctx, task.ID)
			}
			code := "STEP_FAILED"
			if errors.Is(probeErr, context.DeadlineExceeded) {
				code = "STEP_TIMEOUT"
			}
			if isInstallerPanic(probeErr) {
				code = "INSTALLER_PANIC"
			}
			return w.failStep(ctx, task.ID, definition.ID, code, safeMessage(logger, probeErr))
		}
		if !probed {
			runCtx, runCancel := context.WithTimeout(workCtx, stepTimeout(definition))
			runErr := callRun(definition.Run, runCtx, execCtx)
			runCancel()
			if runErr != nil {
				if errors.Is(runErr, context.Canceled) && w.cancelRequested(ctx, task.ID) {
					return w.finishCancelled(ctx, task.ID)
				}
				code := "STEP_FAILED"
				if errors.Is(runErr, context.DeadlineExceeded) {
					code = "STEP_TIMEOUT"
				}
				if isInstallerPanic(runErr) {
					code = "INSTALLER_PANIC"
				}
				return w.failStep(ctx, task.ID, definition.ID, code, safeMessage(logger, runErr))
			}
		}
		typeName := "step_completed"
		if probed {
			typeName = "step_skipped"
		}
		if err := w.repo.CompleteStep(ctx, task.ID, definition.ID, probed, definition.Percent, EventInput{Type: typeName, Owner: w.owner}); err != nil {
			return err
		}
	}
	if len(renewLost) > 0 {
		return ErrLeaseLost
	}
	return w.repo.Finish(ctx, task.ID, TaskSucceeded, "", "", EventInput{Type: "finished", Owner: w.owner})
}

func (w *Worker) renewLease(ctx context.Context, taskID string, stop <-chan struct{}, lost chan<- struct{}, cancel context.CancelFunc) {
	interval := w.lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.repo.RenewLease(ctx, taskID, w.owner, w.lease); err != nil {
				select {
				case lost <- struct{}{}:
				default:
				}
				cancel()
				return
			}
			if task, err := w.repo.GetTask(ctx, taskID); err == nil && task.CancelRequested {
				cancel()
				return
			}
		}
	}
}

func (w *Worker) cancelRequested(ctx context.Context, taskID string) bool {
	task, err := w.repo.GetTask(ctx, taskID)
	return err == nil && task.CancelRequested
}

func (w *Worker) failTask(ctx context.Context, task Task, code, message string) error {
	if err := w.repo.Finish(ctx, task.ID, TaskFailed, code, truncateMessage(message), EventInput{Type: "finished", Owner: w.owner}); err != nil {
		return err
	}
	return nil
}
func (w *Worker) failStep(ctx context.Context, taskID, stepID, code, message string) error {
	if err := w.repo.FailStep(ctx, taskID, stepID, truncateMessage(message), EventInput{Type: "step_failed", Owner: w.owner}); err != nil {
		return err
	}
	return w.repo.Finish(ctx, taskID, TaskFailed, code, truncateMessage(message), EventInput{Type: "finished", Owner: w.owner})
}
func (w *Worker) finishCancelled(ctx context.Context, taskID string) error {
	return w.repo.Finish(ctx, taskID, TaskCancelled, "CANCELLED", "deployment cancelled", EventInput{Type: "finished", Owner: w.owner})
}

func callProbe(fn func(context.Context, ExecutionContext) (bool, error), ctx context.Context, execCtx ExecutionContext) (ok bool, err error) {
	if fn == nil {
		return false, nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("installer panic: %v", recovered)
		}
	}()
	return fn(ctx, execCtx)
}
func callRun(fn func(context.Context, ExecutionContext) error, ctx context.Context, execCtx ExecutionContext) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("installer panic: %v", recovered)
		}
	}()
	return fn(ctx, execCtx)
}
func isInstallerPanic(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "installer panic:")
}
func stepTimeout(step StepDefinition) time.Duration {
	if step.Timeout > 0 {
		return step.Timeout
	}
	return 5 * time.Minute
}
func validateWorkerPlan(plan []StepDefinition) error {
	if len(plan) == 0 {
		return ErrInvalidInput
	}
	last := 0
	seen := map[string]bool{}
	for _, step := range plan {
		if step.ID == "" || seen[step.ID] || step.Percent <= last || step.Percent > 100 {
			return ErrInvalidInput
		}
		seen[step.ID] = true
		last = step.Percent
	}
	if last != 100 {
		return ErrInvalidInput
	}
	return nil
}
func safeMessage(logger *RedactingLogger, err error) string {
	if err == nil {
		return ""
	}
	return truncateMessage(logger.Redact(err.Error()))
}
func truncateMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}

func scalarSecrets(data []byte) []string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				out = append(out, x)
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(value)
	return out
}
