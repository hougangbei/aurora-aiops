package aiops

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from Status
		to   Status
		want bool
	}{
		// Valid forward edges from the design spec (4.1)
		{StatusReceived, StatusTriaging, true},
		{StatusTriaging, StatusCollecting, true},
		{StatusCollecting, StatusAnalyzing, true},
		{StatusAnalyzing, StatusProposing, true},
		{StatusProposing, StatusAwaitingApproval, true},
		{StatusAwaitingApproval, StatusApproved, true},
		{StatusApproved, StatusExecuting, true},
		{StatusExecuting, StatusResolved, true},
		{StatusProposing, StatusRejected, true},

		// All non-terminal states can transition to failed
		{StatusReceived, StatusFailed, true},
		{StatusTriaging, StatusFailed, true},
		{StatusCollecting, StatusFailed, true},
		{StatusAnalyzing, StatusFailed, true},
		{StatusProposing, StatusFailed, true},
		{StatusAwaitingApproval, StatusFailed, true},
		{StatusApproved, StatusFailed, true},
		{StatusExecuting, StatusFailed, true},

		// Invalid transitions
		{StatusReceived, StatusExecuting, false},
		{StatusRejected, StatusExecuting, false},

		// Terminal states cannot transition out
		{StatusResolved, StatusFailed, false},
		{StatusRejected, StatusTriaging, false},
		{StatusFailed, StatusReceived, false},

		// Self-loops are always false
		{StatusReceived, StatusReceived, false},
		{StatusTriaging, StatusTriaging, false},
		{StatusResolved, StatusResolved, false},
		{StatusFailed, StatusFailed, false},

		// Additional undefined edges
		{StatusResolved, StatusTriaging, false},
		{StatusRejected, StatusReceived, false},
	}
	for _, tt := range tests {
		got := CanTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
