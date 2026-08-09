package aiops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/evidence"
	"github.com/heihuzicity-tech/kubejojo/server/internal/llm"
)

// errWorkflowStopped signals a step ended the run (role failure recorded, or
// model_unavailable) without an infrastructure error. Run returns nil.
var errWorkflowStopped = errors.New("workflow stopped (failure recorded)")

// roleStep maps a diagnostic role to the incident statuses it runs under and
// advances to. The five roles advance the incident along the whitelist:
// received -> triaging -> collecting -> analyzing -> proposing ->
// awaiting_approval.
type roleStep struct {
	role        string
	startStatus Status
	nextStatus  Status
}

var workflowSteps = []roleStep{
	{role: "triage", startStatus: StatusReceived, nextStatus: StatusTriaging},
	{role: "collector", startStatus: StatusTriaging, nextStatus: StatusCollecting},
	{role: "root_cause", startStatus: StatusCollecting, nextStatus: StatusAnalyzing},
	{role: "remediation", startStatus: StatusAnalyzing, nextStatus: StatusProposing},
	{role: "risk_review", startStatus: StatusProposing, nextStatus: StatusAwaitingApproval},
}

func requiresModel(role string) bool {
	switch role {
	case "triage", "collector":
		return false
	default:
		return true
	}
}

func currentStep(status Status) (roleStep, bool) {
	for _, step := range workflowSteps {
		if step.startStatus == status {
			return step, true
		}
	}
	return roleStep{}, false
}

// LLMClient is the minimal model surface the workflow needs.
type LLMClient interface {
	GenerateJSON(ctx context.Context, request llm.Request) (llm.Response, error)
}

// WorkflowOptions wires the recoverable diagnostic workflow.
type WorkflowOptions struct {
	DB              *sql.DB
	Incidents       IncidentRepository
	Runs            RunRepository
	Evidence        evidence.Repository
	Collector       evidence.Collector
	LLM             LLMClient // nil when no model is configured
	ModelConfigured bool
	Model           string
	// Events, when non-nil, receives run_started/run_completed and incident
	// events; every event is persisted before it is broadcast.
	Events *EventStore
}

// Workflow runs the five diagnostic roles for an incident in strict order,
// recording each as an AgentRun. It is resumable: completed runs are never
// re-executed, and a role that fails records the failure and moves the incident
// to the failed terminal state without executing any action.
type Workflow struct {
	db              *sql.DB
	incidents       IncidentRepository
	runs            RunRepository
	evidence        evidence.Repository
	collector       evidence.Collector
	llm             LLMClient
	modelConfigured bool
	model           string
	events          *EventStore
}

// NewWorkflow returns a workflow bound to the given dependencies.
func NewWorkflow(opts WorkflowOptions) *Workflow {
	return &Workflow{
		db:              opts.DB,
		incidents:       opts.Incidents,
		runs:            opts.Runs,
		evidence:        opts.Evidence,
		collector:       opts.Collector,
		llm:             opts.LLM,
		modelConfigured: opts.ModelConfigured,
		model:           opts.Model,
		events:          opts.Events,
	}
}

// ModelStatus exposes only the non-secret runtime model state needed by the
// operator UI. Endpoint URLs and credentials never leave the server.
func (w *Workflow) ModelStatus() (configured bool, model string) {
	if w == nil {
		return false, ""
	}
	return w.modelConfigured, w.model
}

// Run drives the incident through every remaining role. It takes a per-incident
// workflow lock so concurrent triggers for the same incident are serialized
// (the loser returns ErrWorkflowLocked). The incident's current status decides
// where to resume: roles whose AgentRun already succeeded are skipped, so a
// restart never recollects or re-diagnoses finished steps.
func (w *Workflow) Run(ctx context.Context, incidentID string) error {
	locked, err := w.acquireLock(ctx, incidentID)
	if err != nil {
		return err
	}
	if !locked {
		return ErrWorkflowLocked
	}
	defer w.releaseLock(ctx, incidentID)
	return w.runLocked(ctx, incidentID)
}

// runLocked executes the role chain assuming the workflow lock is held.
func (w *Workflow) runLocked(ctx context.Context, incidentID string) error {
	inc, err := w.incidents.Get(ctx, incidentID)
	if err != nil {
		return err
	}

	for {
		step, ok := currentStep(inc.Status)
		if !ok {
			return nil // all roles done or terminal state
		}

		run, err := w.beginRun(ctx, inc, step)
		if err != nil {
			return err
		}

		w.emit(ctx, inc.ID, EventRunStarted, mustJSON(map[string]any{
			"id": run.ID, "role": run.Role, "attempt": run.Attempt,
		}))

		err = w.executeStep(ctx, &run, inc, step)

		w.emit(ctx, inc.ID, EventRunCompleted, mustJSON(map[string]any{
			"id": run.ID, "role": run.Role, "status": string(run.Status),
			"summary": run.Summary, "error": run.Error,
		}))

		switch {
		case err == nil:
			// continue to the next role
		case errors.Is(err, errWorkflowStopped):
			return nil
		default:
			return fmt.Errorf("execute %s for incident %s: %w", step.role, incidentID, err)
		}

		inc, err = w.incidents.Get(ctx, incidentID)
		if err != nil {
			return err
		}
		w.emit(ctx, inc.ID, EventIncidentUpdated, mustJSON(map[string]any{
			"id": inc.ID, "status": string(inc.Status),
		}))
	}
}

// Reanalyze restarts the diagnostic workflow for a terminal incident. It clears
// the collected evidence, resets the incident to received, and re-runs every
// role. Only failed, rejected, and resolved incidents may be reanalyzed.
func (w *Workflow) Reanalyze(ctx context.Context, incidentID string) error {
	locked, err := w.acquireLock(ctx, incidentID)
	if err != nil {
		return err
	}
	if !locked {
		return ErrWorkflowLocked
	}
	defer w.releaseLock(ctx, incidentID)

	inc, err := w.incidents.Get(ctx, incidentID)
	if err != nil {
		return err
	}
	switch inc.Status {
	case StatusFailed, StatusRejected, StatusResolved:
	default:
		return ErrReanalyzeNotAllowed
	}

	w.emit(ctx, incidentID, EventReanalyzeStarted, mustJSON(map[string]any{"id": incidentID}))

	if err := w.evidence.DeleteIncidentEvidence(ctx, incidentID); err != nil {
		return err
	}
	if err := w.incidents.UpdateStatus(ctx, incidentID, StatusReceived, time.Now().UTC()); err != nil {
		return err
	}
	return w.runLocked(ctx, incidentID)
}

// EvidenceFor returns the persisted evidence DAG for an incident.
func (w *Workflow) EvidenceFor(ctx context.Context, incidentID string) ([]evidence.Node, []evidence.Edge, error) {
	nodes, err := w.evidence.ListNodes(ctx, incidentID)
	if err != nil {
		return nil, nil, err
	}
	edges, err := w.evidence.ListEdges(ctx, incidentID)
	if err != nil {
		return nil, nil, err
	}
	return nodes, edges, nil
}

// RunsFor returns the persisted agent runs for an incident.
func (w *Workflow) RunsFor(ctx context.Context, incidentID string) ([]AgentRun, error) {
	return w.runs.ListRuns(ctx, incidentID)
}

// emit persists and broadcasts an event, ignoring failures: events must never
// block the workflow.
func (w *Workflow) emit(ctx context.Context, incidentID string, typ EventType, data string) {
	if w.events == nil {
		return
	}
	_, _ = w.events.Append(ctx, incidentID, typ, data)
}

// acquireLock inserts the per-incident lock row. It returns false when another
// run holds the lock.
func (w *Workflow) acquireLock(ctx context.Context, incidentID string) (bool, error) {
	res, err := w.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO workflow_locks (incident_id, acquired_at) VALUES (?, ?)`,
		incidentID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("acquire workflow lock: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (w *Workflow) releaseLock(ctx context.Context, incidentID string) {
	_, _ = w.db.ExecContext(ctx, `DELETE FROM workflow_locks WHERE incident_id = ?`, incidentID)
}

func (w *Workflow) beginRun(ctx context.Context, inc Incident, step roleStep) (AgentRun, error) {
	attempt, err := w.runs.NextAttempt(ctx, inc.ID, step.role)
	if err != nil {
		return AgentRun{}, err
	}
	id, err := generateRunID()
	if err != nil {
		return AgentRun{}, err
	}
	run := AgentRun{
		ID:         id,
		IncidentID: inc.ID,
		Role:       step.role,
		Attempt:    attempt,
		Status:     RunStatusRunning,
		StartedAt:  time.Now().UTC(),
	}
	if err := w.runs.CreateRun(ctx, run); err != nil {
		return AgentRun{}, err
	}
	return run, nil
}

func (w *Workflow) executeStep(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	if !w.modelConfigured && requiresModel(step.role) {
		return w.skipModelUnavailable(ctx, run, inc)
	}

	switch step.role {
	case "triage":
		return w.runTriage(ctx, run, inc, step)
	case "collector":
		return w.runCollector(ctx, run, inc, step)
	case "root_cause":
		return w.runRootCause(ctx, run, inc, step)
	case "remediation":
		return w.runRemediation(ctx, run, inc, step)
	case "risk_review":
		return w.runRiskReview(ctx, run, inc, step)
	default:
		return fmt.Errorf("unknown role %q", step.role)
	}
}

// skipModelUnavailable records a skipped run for a model-backed role when no
// model is configured, then stops the workflow: the incident stays where it is
// (deterministic triage/collector finished) and no further role runs.
func (w *Workflow) skipModelUnavailable(ctx context.Context, run *AgentRun, inc Incident) error {
	run.Status = RunStatusSkipped
	run.Summary = ModelUnavailable
	if err := w.runs.CompleteRun(ctx, *run, "", time.Now().UTC()); err != nil {
		return err
	}
	return errWorkflowStopped
}

// failRun records a role failure, moves the incident to the terminal failed
// state, and stops the workflow. No remediation action is ever executed from
// an invalid or errored model output.
func (w *Workflow) failRun(ctx context.Context, run *AgentRun, cause error) error {
	run.Status = RunStatusFailed
	run.Error = cause.Error()
	if err := w.runs.CompleteRun(ctx, *run, StatusFailed, time.Now().UTC()); err != nil {
		return err
	}
	return errWorkflowStopped
}

// succeedRun records a successful role and advances the incident.
func (w *Workflow) succeedRun(ctx context.Context, run *AgentRun, step roleStep) error {
	run.Status = RunStatusSucceeded
	return w.runs.CompleteRun(ctx, *run, step.nextStatus, time.Now().UTC())
}

func (w *Workflow) runTriage(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	if !w.modelConfigured {
		out := DeterministicTriage(inc)
		run.Summary = out.Summary
		run.Output = mustJSON(out)
		return w.succeedRun(ctx, run, step)
	}
	resp, err := w.llm.GenerateJSON(ctx, w.chatRequest(BuildTriagePrompt(formatBundle(w.buildBundle(ctx, inc)))))
	if err != nil {
		return w.failRun(ctx, run, fmt.Errorf("llm triage: %w", err))
	}
	out, err := DecodeTriage(resp.Text)
	if err != nil {
		return w.failRun(ctx, run, err)
	}
	applyModelUsage(run, resp)
	run.Summary = out.Summary
	run.Output = mustJSON(out)
	return w.succeedRun(ctx, run, step)
}

func (w *Workflow) runCollector(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	// Deterministic Kubernetes evidence collection always runs; optional
	// capability failures yield partial evidence and are recorded, not fatal.
	// Collection takes the recent tail of logs (TailLines=200) rather than a
	// SinceTime anchored at incident creation: on a fresh incident that would
	// point at "now" and yield near-empty logs.
	nodes, edges, collectErr := w.collector.Collect(ctx, evidence.Target{
		Namespace: inc.Namespace,
		Kind:      inc.ResourceKind,
		Name:      inc.ResourceName,
	}, evidence.Window{})

	var partial []string
	if collectErr != nil {
		var colErr *evidence.CollectionError
		if errors.As(collectErr, &colErr) {
			partial = append(partial, colErr.Failed...)
		} else {
			return w.failRun(ctx, run, fmt.Errorf("collect evidence: %w", collectErr))
		}
	}

	for i := range nodes {
		nodes[i].IncidentID = inc.ID
		if err := w.evidence.AddNode(ctx, nodes[i]); err != nil && !errors.Is(err, evidence.ErrNodeExists) {
			return w.failRun(ctx, run, err)
		}
	}
	for i := range edges {
		edges[i].IncidentID = inc.ID
		if err := w.evidence.AddEdge(ctx, edges[i]); err != nil && !errors.Is(err, evidence.ErrEdgeExists) {
			return w.failRun(ctx, run, err)
		}
	}

	var out CollectorOutput
	if w.modelConfigured {
		resp, err := w.llm.GenerateJSON(ctx, w.chatRequest(BuildCollectorPrompt(formatBundle(w.buildBundle(ctx, inc)))))
		if err != nil {
			return w.failRun(ctx, run, fmt.Errorf("llm collector: %w", err))
		}
		decoded, err := DecodeCollector(resp.Text)
		if err != nil {
			return w.failRun(ctx, run, err)
		}
		out = decoded
		applyModelUsage(run, resp)
	}

	run.Summary = fmt.Sprintf("collected %d evidence nodes", len(nodes))
	if len(partial) > 0 {
		run.Summary += "; " + strings.Join(partial, "; ")
	}
	run.Output = mustJSON(out)
	return w.succeedRun(ctx, run, step)
}

func (w *Workflow) runRootCause(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	bundle := w.buildBundle(ctx, inc)
	validIDs := evidenceIDs(bundle.Nodes)
	resp, err := w.llm.GenerateJSON(ctx, w.chatRequest(BuildRootCausePrompt(formatBundle(bundle))))
	if err != nil {
		return w.failRun(ctx, run, fmt.Errorf("llm root cause: %w", err))
	}
	out, err := DecodeRootCause(resp.Text, validIDs)
	if err != nil {
		return w.failRun(ctx, run, err)
	}
	applyModelUsage(run, resp)
	run.Summary = fmt.Sprintf("%d root cause candidate(s)", len(out.Candidates))
	run.Output = mustJSON(out)
	return w.succeedRun(ctx, run, step)
}

func (w *Workflow) runRemediation(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	bundle := w.buildBundle(ctx, inc)
	rootCause := w.latestRootCause(ctx, inc.ID)
	resp, err := w.llm.GenerateJSON(ctx, w.chatRequest(BuildRemediationPrompt(formatBundle(bundle), rootCause)))
	if err != nil {
		return w.failRun(ctx, run, fmt.Errorf("llm remediation: %w", err))
	}
	out, err := DecodeRemediation(resp.Text)
	if err != nil {
		return w.failRun(ctx, run, err)
	}
	applyModelUsage(run, resp)
	run.Summary = fmt.Sprintf("%d remediation action(s)", len(out.Actions))
	run.Output = mustJSON(out)
	return w.succeedRun(ctx, run, step)
}

func (w *Workflow) runRiskReview(ctx context.Context, run *AgentRun, inc Incident, step roleStep) error {
	bundle := w.buildBundle(ctx, inc)
	remediation := w.latestRemediation(ctx, inc.ID)
	resp, err := w.llm.GenerateJSON(ctx, w.chatRequest(BuildRiskReviewPrompt(formatBundle(bundle), remediation)))
	if err != nil {
		return w.failRun(ctx, run, fmt.Errorf("llm risk review: %w", err))
	}
	modelReview, err := DecodeRiskReview(resp.Text)
	if err != nil {
		return w.failRun(ctx, run, err)
	}
	effective := BuildEffectiveRiskReview(modelReview, remediation, inc.Namespace)
	applyModelUsage(run, resp)
	run.Summary = fmt.Sprintf("effective risk %s, approvable=%t", effective.EffectiveRisk, effective.Approvable)
	run.Output = mustJSON(effective)
	return w.succeedRun(ctx, run, step)
}

func (w *Workflow) chatRequest(prompt string) llm.Request {
	return llm.Request{
		Model:    w.model,
		Messages: []llm.Message{{Role: "user", Content: prompt}},
		JSONMode: true,
	}
}

func (w *Workflow) buildBundle(ctx context.Context, inc Incident) evidence.Bundle {
	nodes, _ := w.evidence.ListNodes(ctx, inc.ID)
	edges, _ := w.evidence.ListEdges(ctx, inc.ID)
	ref := evidence.IncidentReference{
		ID:           inc.ID,
		Summary:      inc.Summary,
		Severity:     string(inc.Severity),
		Status:       string(inc.Status),
		Namespace:    inc.Namespace,
		ResourceKind: inc.ResourceKind,
		ResourceName: inc.ResourceName,
		CreatedAt:    inc.CreatedAt,
		UpdatedAt:    inc.UpdatedAt,
	}
	return evidence.BuildBundle(ref, nodes, edges)
}

// latestRootCause decodes the most recent succeeded root_cause output for the
// remediation prompt.
func (w *Workflow) latestRootCause(ctx context.Context, incidentID string) []RootCauseCandidate {
	runs, err := w.runs.ListRuns(ctx, incidentID)
	if err != nil {
		return nil
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Role == "root_cause" && runs[i].Status == RunStatusSucceeded {
			var out RootCauseOutput
			if json.Unmarshal([]byte(runs[i].Output), &out) == nil {
				return out.Candidates
			}
		}
	}
	return nil
}

// latestRemediation decodes the most recent succeeded remediation output for
// the risk review prompt.
func (w *Workflow) latestRemediation(ctx context.Context, incidentID string) RemediationOutput {
	runs, err := w.runs.ListRuns(ctx, incidentID)
	if err != nil {
		return RemediationOutput{}
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Role == "remediation" && runs[i].Status == RunStatusSucceeded {
			var out RemediationOutput
			if json.Unmarshal([]byte(runs[i].Output), &out) == nil {
				return out
			}
		}
	}
	return RemediationOutput{}
}

func evidenceIDs(nodes []evidence.Node) map[string]bool {
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
	}
	return ids
}

func applyModelUsage(run *AgentRun, resp llm.Response) {
	run.Model = resp.Model
	run.PromptTokens = resp.Usage.PromptTokens
	run.CompletionTokens = resp.Usage.CompletionTokens
	run.TotalTokens = resp.Usage.TotalTokens
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
