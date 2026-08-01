package evidence

import (
	"context"
	"strings"
	"time"
)

// Target identifies the Kubernetes resource to collect evidence for.
type Target struct {
	Namespace string
	Kind      string
	Name      string
}

// Window bounds the collection window. A zero Start means no lower bound; the
// End field is reserved for future use and currently unenforced.
type Window struct {
	Start time.Time
	End   time.Time
}

// Collector collects evidence for a target.
type Collector interface {
	Collect(ctx context.Context, target Target, window Window) ([]Node, []Edge, error)
}

// CollectionError aggregates failures of optional collection surfaces (for
// example the Metrics API). Partial evidence is still returned alongside it;
// the workflow may continue with what was collected.
type CollectionError struct {
	Failed []string
}

func (e *CollectionError) Error() string {
	if e == nil {
		return ""
	}
	return "partial evidence: " + strings.Join(e.Failed, "; ")
}
