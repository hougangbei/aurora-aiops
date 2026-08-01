package aiops

// transitions defines the allowed state transitions for incidents.
// Each key is a source status; the value is the list of statuses
// that may follow it.
var transitions = map[Status][]Status{
	StatusReceived:         {StatusTriaging, StatusFailed},
	StatusTriaging:         {StatusCollecting, StatusFailed},
	StatusCollecting:       {StatusAnalyzing, StatusFailed},
	StatusAnalyzing:        {StatusProposing, StatusFailed},
	StatusProposing:        {StatusAwaitingApproval, StatusRejected, StatusFailed},
	StatusAwaitingApproval: {StatusApproved, StatusFailed},
	StatusApproved:         {StatusExecuting, StatusFailed},
	StatusExecuting:        {StatusResolved, StatusFailed},
	// Terminal states: resolved, rejected, and failed have no outgoing edges.
}

// CanTransition reports whether an incident may move from one status to another.
// Self-loops (from == to) always return false.
// Terminal states (resolved, rejected, failed) cannot transition out.
func CanTransition(from, to Status) bool {
	if from == to {
		return false
	}
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}
