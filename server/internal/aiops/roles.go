package aiops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ModelUnavailable is the agent run status recorded when no model is
// configured: deterministic triage/collector still run, model-backed roles do
// not.
const ModelUnavailable = "model_unavailable"

// ErrInvalidRoleOutput marks role output that fails JSON decoding or semantic
// validation. The workflow treats it as a safe failure: no action is taken.
var ErrInvalidRoleOutput = errors.New("invalid role output")

// TriageOutput is the decision of the triage role.
type TriageOutput struct {
	Summary   string `json:"summary"`
	Severity  string `json:"severity"` // info | warning | critical
	Rationale string `json:"rationale,omitempty"`
}

// CollectorOutput is the collection plan of the collector role.
type CollectorOutput struct {
	TargetKind string   `json:"targetKind"`
	TargetName string   `json:"targetName"`
	Commands   []string `json:"commands,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

// RootCauseOutput is the decision of the root cause role.
type RootCauseOutput struct {
	Candidates []RootCauseCandidate `json:"candidates"`
}

// RootCauseCandidate is one hypothesized root cause tied to collected
// evidence.
type RootCauseCandidate struct {
	Summary           string   `json:"summary"`
	Confidence        float64  `json:"confidence"`
	EvidenceIDs       []string `json:"evidenceIds"`
	VerificationSteps []string `json:"verificationSteps,omitempty"`
}

// RemediationAction is one remediation step proposed by the remediation role.
// Command is a display summary; the structured fields (Kind, Namespace,
// ResourceKind, ResourceName, Parameters) are what the executor acts on after
// policy validation. Structured fields may be absent for display-only actions.
type RemediationAction struct {
	Command      string            `json:"command"`
	Reason       string            `json:"reason"`
	Risk         string            `json:"risk"` // low | medium | high
	Kind         string            `json:"kind,omitempty"`
	Namespace    string            `json:"namespace,omitempty"`
	ResourceKind string            `json:"resourceKind,omitempty"`
	ResourceName string            `json:"resourceName,omitempty"`
	Parameters   map[string]string `json:"parameters,omitempty"`
}

// RemediationOutput is the decision of the remediation role.
type RemediationOutput struct {
	Actions []RemediationAction `json:"actions"`
}

// RiskReviewOutput is the decision of the risk review role.
type RiskReviewOutput struct {
	RiskLevel string   `json:"riskLevel"` // low | medium | high | critical
	Approved  bool     `json:"approved"`
	Blockers  []string `json:"blockers,omitempty"`
	Rationale string   `json:"rationale,omitempty"`
}

// PolicyActionReview is the deterministic per-action policy verdict surfaced by
// the effective risk review. It is computed from policy.Evaluate, never from the
// model's per-action risk field.
type PolicyActionReview struct {
	Kind         string `json:"kind"`
	ResourceKind string `json:"resourceKind"`
	ResourceName string `json:"resourceName"`
	Allowed      bool   `json:"allowed"`
	Risk         string `json:"risk"`
	Reason       string `json:"reason"`
}

// EffectiveRiskReview is the only source of approvability. ModelReview is
// retained nested for explanation but never changes EffectiveRisk or Approvable,
// which are derived entirely from the deterministic policy evaluation.
type EffectiveRiskReview struct {
	EffectiveRisk string               `json:"effectiveRisk"`
	Approvable    bool                 `json:"approvable"`
	Blockers      []string             `json:"blockers,omitempty"`
	Actions       []PolicyActionReview `json:"actions"`
	ModelReview   RiskReviewOutput     `json:"modelReview"`
}

// decodeJSON strips markdown fences, then decodes text as T. Fence stripping
// tolerates trailing backticks so models may wrap output in ```json blocks.
func decodeJSON[T any](text string) (T, error) {
	var out T
	cleaned := strings.TrimSpace(text)
	if strings.HasPrefix(cleaned, "```") {
		if lines := strings.SplitN(cleaned, "\n", 2); len(lines) == 2 {
			cleaned = strings.TrimSuffix(strings.TrimSpace(lines[1]), "```")
		}
	}
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidRoleOutput, err)
	}
	return out, nil
}

// DecodeTriage parses and validates a triage output.
func DecodeTriage(text string) (TriageOutput, error) {
	out, err := decodeJSON[TriageOutput](text)
	if err != nil {
		return TriageOutput{}, err
	}
	if strings.TrimSpace(out.Summary) == "" {
		return TriageOutput{}, fmt.Errorf("%w: empty summary", ErrInvalidRoleOutput)
	}
	if !oneOf(out.Severity, "info", "warning", "critical") {
		return TriageOutput{}, fmt.Errorf("%w: severity %q", ErrInvalidRoleOutput, out.Severity)
	}
	return out, nil
}

// DecodeCollector parses and validates a collector output.
func DecodeCollector(text string) (CollectorOutput, error) {
	out, err := decodeJSON[CollectorOutput](text)
	if err != nil {
		return CollectorOutput{}, err
	}
	if strings.TrimSpace(out.TargetKind) == "" || strings.TrimSpace(out.TargetName) == "" {
		return CollectorOutput{}, fmt.Errorf("%w: empty target", ErrInvalidRoleOutput)
	}
	return out, nil
}

// DecodeRootCause parses and validates a root cause output against the set of
// evidence IDs that actually exist for the incident. Unknown references and
// out-of-range confidence are rejected so the workflow never acts on a model
// that hallucinated evidence.
func DecodeRootCause(text string, validEvidenceIDs map[string]bool) (RootCauseOutput, error) {
	out, err := decodeJSON[RootCauseOutput](text)
	if err != nil {
		return RootCauseOutput{}, err
	}
	if len(out.Candidates) == 0 {
		return RootCauseOutput{}, fmt.Errorf("%w: empty candidates", ErrInvalidRoleOutput)
	}
	for _, c := range out.Candidates {
		if c.Confidence < 0 || c.Confidence > 1 {
			return RootCauseOutput{}, fmt.Errorf("%w: confidence %v out of range", ErrInvalidRoleOutput, c.Confidence)
		}
		if len(c.EvidenceIDs) == 0 {
			return RootCauseOutput{}, fmt.Errorf("%w: candidate %q has no evidence", ErrInvalidRoleOutput, c.Summary)
		}
		for _, id := range c.EvidenceIDs {
			if !validEvidenceIDs[id] {
				return RootCauseOutput{}, fmt.Errorf("%w: unknown evidence id %q", ErrInvalidRoleOutput, id)
			}
		}
	}
	return out, nil
}

// shellMetachars mark a command as a shell string rather than a single
// parameterized action. Remediation actions must never chain commands, pipe,
// redirect, or eval.
var shellMetachars = []string{
	";", "&&", "||", "|", ">", "<", "`", "$(", "${",
	"eval", "bash -c", "sh -c", "rm -rf", "rm -fr",
}

func isShellString(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, marker := range shellMetachars {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// DecodeRemediation parses and validates a remediation output. Shell-string
// commands are rejected outright.
func DecodeRemediation(text string) (RemediationOutput, error) {
	out, err := decodeJSON[RemediationOutput](text)
	if err != nil {
		return RemediationOutput{}, err
	}
	if len(out.Actions) == 0 {
		return RemediationOutput{}, fmt.Errorf("%w: empty actions", ErrInvalidRoleOutput)
	}
	for _, a := range out.Actions {
		if strings.TrimSpace(a.Command) == "" {
			return RemediationOutput{}, fmt.Errorf("%w: empty command", ErrInvalidRoleOutput)
		}
		if isShellString(a.Command) {
			return RemediationOutput{}, fmt.Errorf("%w: shell string command %q", ErrInvalidRoleOutput, a.Command)
		}
		if !oneOf(a.Risk, "low", "medium", "high") {
			return RemediationOutput{}, fmt.Errorf("%w: risk %q", ErrInvalidRoleOutput, a.Risk)
		}
		// 结构化字段可选：一旦给出 Kind，其余字段必须完整，否则执行阶段无法通过 policy。
		if a.Kind != "" {
			if strings.TrimSpace(a.Namespace) == "" ||
				strings.TrimSpace(a.ResourceKind) == "" ||
				strings.TrimSpace(a.ResourceName) == "" {
				return RemediationOutput{}, fmt.Errorf("%w: structured action %q is incomplete", ErrInvalidRoleOutput, a.Kind)
			}
		}
	}
	return out, nil
}

// DecodeRiskReview parses and validates a risk review output.
func DecodeRiskReview(text string) (RiskReviewOutput, error) {
	out, err := decodeJSON[RiskReviewOutput](text)
	if err != nil {
		return RiskReviewOutput{}, err
	}
	if !oneOf(out.RiskLevel, "low", "medium", "high", "critical") {
		return RiskReviewOutput{}, fmt.Errorf("%w: riskLevel %q", ErrInvalidRoleOutput, out.RiskLevel)
	}
	return out, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

// DeterministicTriage produces a triage decision without a model, using only
// the incident record already present. Used when no model is configured.
func DeterministicTriage(inc Incident) TriageOutput {
	return TriageOutput{
		Summary:   inc.Summary,
		Severity:  string(inc.Severity),
		Rationale: "deterministic triage (no model configured)",
	}
}

// DeterministicCollector produces a collection plan without a model. The
// commands are descriptive labels, never shell strings.
func DeterministicCollector(inc Incident) CollectorOutput {
	return CollectorOutput{
		TargetKind: inc.ResourceKind,
		TargetName: inc.ResourceName,
		Commands:   []string{"collect resource snapshot", "collect associated events", "collect tail logs", "collect pod metrics"},
		Notes:      "deterministic collector (no model configured)",
	}
}
