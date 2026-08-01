package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// NodeKind categorizes a single piece of evidence collected for an incident.
type NodeKind string

const (
	NodeKindSnapshot NodeKind = "snapshot"
	NodeKindEvent    NodeKind = "event"
	NodeKindLog      NodeKind = "log"
	NodeKindMetric   NodeKind = "metric"
	NodeKindAgent    NodeKind = "agent"
	NodeKindSystem   NodeKind = "system"
)

// Node is one vertex in the incident's evidence DAG. Hash is a SHA-256 hex
// digest of the normalized payload, computed by the repository at insert time
// when the caller leaves it empty.
type Node struct {
	IncidentID string    `json:"incidentId"`
	ID         string    `json:"id"`
	Kind       NodeKind  `json:"kind"`
	Payload    string    `json:"payload"`
	ObservedAt time.Time `json:"observedAt"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Relation describes how the From node relates to the To node.
type Relation string

const (
	RelationSupports    Relation = "supports"
	RelationContradicts Relation = "contradicts"
)

// Edge is one directed relationship between two evidence nodes of an incident.
type Edge struct {
	IncidentID string    `json:"incidentId"`
	FromID     string    `json:"fromId"`
	ToID       string    `json:"toId"`
	Relation   Relation  `json:"relation"`
	CreatedAt  time.Time `json:"createdAt"`
}

// normalizePayload produces a canonical form of a payload: surrounding
// whitespace is trimmed, and JSON values are re-encoded with sorted object
// keys while preserving number precision (json.Number), so semantically
// identical payloads normalize identically. Non-JSON payloads are returned
// trimmed.
func normalizePayload(payload string) string {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err == nil {
			if canonical, err := json.Marshal(v); err == nil {
				return string(canonical)
			}
		}
	}
	return trimmed
}

// StableHash returns the SHA-256 hex digest of the normalized payload. Two
// semantically identical payloads always produce the same hash; the raw
// evidence text itself is not required to match byte-for-byte.
func StableHash(payload string) string {
	sum := sha256.Sum256([]byte(normalizePayload(payload)))
	return hex.EncodeToString(sum[:])
}
