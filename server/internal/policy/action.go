package policy

// Action is a structured remediation action. It deliberately has no Command
// field: shell strings are never accepted as remediation; the executor maps
// each Kind to a single client-go write operation.
type Action struct {
	Kind         string            `json:"kind"`
	Namespace    string            `json:"namespace"`
	ResourceKind string            `json:"resourceKind"`
	ResourceName string            `json:"resourceName"`
	Parameters   map[string]string `json:"parameters"`
}

// Allowed action kinds. Anything else is rejected by the allowlist.
const (
	ActionRestartDeployment = "restart_deployment"
	ActionScaleDeployment   = "scale_deployment"
	ActionSuspendCronJob    = "suspend_cronjob"
)

// RiskLevel is decided by policy rules, never by the model: Action has no risk
// field.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)
