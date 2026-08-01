package aiops

import "time"

// Severity represents the severity level of an incident.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Status represents the current state of an incident.
type Status string

const (
	StatusReceived         Status = "received"
	StatusTriaging         Status = "triaging"
	StatusCollecting       Status = "collecting"
	StatusAnalyzing        Status = "analyzing"
	StatusProposing        Status = "proposing"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusApproved         Status = "approved"
	StatusExecuting        Status = "executing"
	StatusResolved         Status = "resolved"
	StatusRejected         Status = "rejected"
	StatusFailed           Status = "failed"
)

// Incident represents an AIOps diagnostic incident.
type Incident struct {
	ID           string    `json:"id"`
	Summary      string    `json:"summary"`
	Severity     Severity  `json:"severity"`
	Status       Status    `json:"status"`
	Namespace    string    `json:"namespace"`
	ResourceKind string    `json:"resourceKind"`
	ResourceName string    `json:"resourceName"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
