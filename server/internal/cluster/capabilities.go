package cluster

import "time"

// State is the overall health state of the shared cluster connection.
type State string

const (
	StateConnected   State = "connected"
	StateDegraded    State = "degraded"
	StateUnreachable State = "unreachable"
)

// Capabilities reports which cluster API surfaces were reachable during the
// most recent probe.
type Capabilities struct {
	Nodes   bool `json:"nodes"`
	Events  bool `json:"events"`
	PodLogs bool `json:"podLogs"`
	Metrics bool `json:"metrics"`
}

// CheckResult describes a single capability check performed by a probe.
type CheckResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ProbeResult is the complete outcome of one bounded cluster probe.
type ProbeResult struct {
	State          State                  `json:"state"`
	CurrentContext string                 `json:"currentContext"`
	ServerHost     string                 `json:"serverHost"`
	Version        string                 `json:"version"`
	LatencyMS      int64                  `json:"latencyMs"`
	CheckedAt      time.Time              `json:"checkedAt"`
	Capabilities   Capabilities           `json:"capabilities"`
	Checks         map[string]CheckResult `json:"checks"`
}
