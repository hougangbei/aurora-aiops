package evidence

import (
	"encoding/json"
	"regexp"
	"sort"
	"time"
)

const (
	// MaxLogPayloadBytes caps a single log evidence payload before it enters a
	// bundle. The tail (most recent lines) is preserved.
	MaxLogPayloadBytes = 4 * 1024
	// MaxBundleBytes bounds the serialized context bundle sent to a model.
	MaxBundleBytes = 64 * 1024
)

// IncidentReference is the minimal incident summary carried into a bundle. It
// duplicates the aiops fields on purpose: evidence stays import-independent so
// the aiops workflow can depend on evidence without an import cycle.
type IncidentReference struct {
	ID           string    `json:"id"`
	Summary      string    `json:"summary"`
	Severity     string    `json:"severity"`
	Status       string    `json:"status"`
	Namespace    string    `json:"namespace"`
	ResourceKind string    `json:"resourceKind"`
	ResourceName string    `json:"resourceName"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Bundle is the bounded, deterministic context handed to a diagnostic model.
// Nodes are ordered by observedAt, kind, id; edges by from, to, relation.
// Truncated reports whether evidence was capped or dropped to stay in budget.
type Bundle struct {
	Incident  IncidentReference `json:"incident"`
	Nodes     []Node            `json:"nodes"`
	Edges     []Edge            `json:"edges"`
	Truncated bool              `json:"truncated"`
}

// retentionPriority orders kinds when the budget forces drops: snapshot, event
// and agent output survive the longest, logs and metrics are droppable, and
// synthetic system nodes go first.
func retentionPriority(k NodeKind) int {
	switch k {
	case NodeKindSnapshot, NodeKindEvent, NodeKindAgent:
		return 3
	case NodeKindLog, NodeKindMetric:
		return 1
	default: // NodeKindSystem
		return 0
	}
}

func nodeLess(a, b Node) bool {
	if !a.ObservedAt.Equal(b.ObservedAt) {
		return a.ObservedAt.Before(b.ObservedAt)
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}

func edgeLess(a, b Edge) bool {
	if a.FromID != b.FromID {
		return a.FromID < b.FromID
	}
	if a.ToID != b.ToID {
		return a.ToID < b.ToID
	}
	return a.Relation < b.Relation
}

// tailOf keeps the last max bytes of s, adjusting the cut to a UTF-8 rune
// boundary so the result is never split mid-character.
func tailOf(s string, max int) string {
	if len(s) <= max {
		return s
	}
	start := len(s) - max
	for start > 0 && start < len(s) && s[start]&0xC0 == 0x80 {
		start++
	}
	return s[start:]
}

// BuildBundle builds a deterministic, bounded context bundle. Each payload is
// redacted for suspected secrets, log payloads are capped to MaxLogPayloadBytes
// (tail kept), and the total serialized bundle is kept within MaxBundleBytes by
// dropping lowest-priority nodes first. The bundle is deterministic for the
// same input regardless of input order.
func BuildBundle(ref IncidentReference, nodes []Node, edges []Edge) Bundle {
	b := Bundle{Incident: ref}
	truncated := false

	baseJSON, err := json.Marshal(Bundle{Incident: ref, Nodes: []Node{}, Edges: edges, Truncated: false})
	if err != nil {
		// Bundle contains only marshalable values; fall back to a conservative base.
		baseJSON = []byte(`{"incident":{},"nodes":[],"edges":[],"truncated":false}`)
	}
	// est tracks the final marshalled length (see below: actual length is always
	// <= est), so the greedy keep stays within MaxBundleBytes by construction.
	est := len(baseJSON)

	// sizeOf is the exact serialized length of one node as it appears in Nodes.
	sizeOf := func(n Node) int {
		raw, err := json.Marshal(n)
		if err != nil {
			return len(n.Payload) + 256
		}
		return len(raw)
	}

	type candidate struct {
		node     Node
		size     int
		priority int
	}
	var candidates []candidate
	for _, n := range nodes {
		n.Payload = RedactSecrets(n.Payload)
		if n.Payload == "" {
			truncated = true
			continue
		}
		if n.Kind == NodeKindLog && len(n.Payload) > MaxLogPayloadBytes {
			n.Payload = tailOf(n.Payload, MaxLogPayloadBytes)
			truncated = true
		}
		candidates = append(candidates, candidate{
			node:     n,
			size:     sizeOf(n),
			priority: retentionPriority(n.Kind),
		})
	}

	// Deterministic processing order: lowest retention priority first, ties by
	// canonical node order. Kept nodes are then re-sorted canonically.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return nodeLess(candidates[i].node, candidates[j].node)
	})

	// Greedy keep from the highest priority down. Each kept node adds its
	// serialized length plus a separator; the last array element adds none, and
	// flipping truncated adds at most one byte, so actual length <= est.
	kept := make([]Node, 0, len(candidates))
	for i := len(candidates) - 1; i >= 0; i-- {
		sep := 1
		if len(kept) == 0 {
			sep = 0
		}
		if est+candidates[i].size+sep+1 <= MaxBundleBytes {
			est += candidates[i].size + sep + 1
			kept = append(kept, candidates[i].node)
		} else {
			truncated = true
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return nodeLess(kept[i], kept[j]) })

	// Only keep edges whose endpoints both survived truncation.
	keptIDs := make(map[string]bool, len(kept))
	for _, n := range kept {
		keptIDs[n.ID] = true
	}
	var keptEdges []Edge
	for _, e := range edges {
		if keptIDs[e.FromID] && keptIDs[e.ToID] {
			keptEdges = append(keptEdges, e)
		}
	}
	sort.SliceStable(keptEdges, func(i, j int) bool { return edgeLess(keptEdges[i], keptEdges[j]) })

	b.Nodes = kept
	b.Edges = keptEdges
	b.Truncated = truncated
	return b
}

var (
	secretValueRe = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|password|passwd|access[_-]?key|private[_-]?key|credential|authorization)[^a-z0-9]*[:=][^a-z0-9]*([^"',\s;]+)`)
	bearerRe      = regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._~+/=-]{12,})`)
	bareJWTPartRe = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}`)
	pemKeyRe      = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

// RedactSecrets masks likely secret material in an evidence payload: key=value
// assignments with secret-like keys, Bearer tokens, bare JWT-looking segments,
// and PEM private key blocks. Everything else passes through untouched.
func RedactSecrets(payload string) string {
	if payload == "" {
		return ""
	}
	redacted := secretValueRe.ReplaceAllString(payload, "${1}:[REDACTED]")
	redacted = bearerRe.ReplaceAllString(redacted, "Bearer [REDACTED]")
	redacted = bareJWTPartRe.ReplaceAllString(redacted, "[REDACTED]")
	redacted = pemKeyRe.ReplaceAllString(redacted, "-----BEGIN [REDACTED]-----")
	return redacted
}
