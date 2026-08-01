package evidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func bundleRef() IncidentReference {
	return IncidentReference{
		ID:           "inc-1",
		Summary:      "Pod crash loop",
		Severity:     "critical",
		Status:       "collecting",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
		CreatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
}

func TestBundleDeterministicOrdering(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	ref := bundleRef()
	nodes := []Node{
		{ID: "c", Kind: NodeKindLog, Payload: "c", ObservedAt: t0.Add(2 * time.Second)},
		{ID: "a", Kind: NodeKindMetric, Payload: "a", ObservedAt: t0},
		{ID: "b", Kind: NodeKindLog, Payload: "b", ObservedAt: t0.Add(time.Second)},
	}
	edges := []Edge{
		{FromID: "b", ToID: "c", Relation: RelationSupports},
		{FromID: "a", ToID: "b", Relation: RelationContradicts},
	}

	// Shuffle both inputs; output must be byte-identical.
	shuffledNodes := []Node{nodes[2], nodes[0], nodes[1]}
	shuffledEdges := []Edge{edges[1], edges[0]}

	b1 := BuildBundle(ref, nodes, edges)
	b2 := BuildBundle(ref, shuffledNodes, shuffledEdges)

	j1, err := json.Marshal(b1)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := json.Marshal(b2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(j1, j2) {
		t.Fatalf("non-deterministic bundle:\n%s\n%s", j1, j2)
	}

	want := []string{"a", "b", "c"} // observedAt ASC, then kind, then id
	for i, n := range b1.Nodes {
		if n.ID != want[i] {
			t.Fatalf("nodes[%d].ID=%q want %q", i, n.ID, want[i])
		}
	}
	if b1.Nodes[0].Kind != NodeKindMetric {
		t.Fatalf("expected metric node first, got kind=%s", b1.Nodes[0].Kind)
	}
}

func TestBundleCapsLogPayload(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	big := strings.Repeat("x", MaxLogPayloadBytes*10)
	nodes := []Node{{ID: "n1", Kind: NodeKindLog, Payload: big, ObservedAt: t0}}

	b := BuildBundle(bundleRef(), nodes, nil)
	if len(b.Nodes) != 1 {
		t.Fatalf("log node dropped unexpectedly: %d nodes", len(b.Nodes))
	}
	got := b.Nodes[0].Payload
	if len(got) > MaxLogPayloadBytes {
		t.Fatalf("log payload %d bytes exceeds %d", len(got), MaxLogPayloadBytes)
	}
	if !strings.HasSuffix(got, strings.Repeat("x", 1024)) {
		t.Fatal("log tail was not preserved")
	}
	if !b.Truncated {
		t.Fatal("expected Truncated=true when a log payload was capped")
	}
}

func TestBundleBoundedTotal(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var nodes []Node
	for i := 0; i < 18; i++ {
		nodes = append(nodes, Node{
			ID:         fmt.Sprintf("log%02d", i),
			Kind:       NodeKindLog,
			Payload:    strings.Repeat("L", MaxLogPayloadBytes),
			ObservedAt: t0.Add(time.Duration(i) * time.Second),
		})
	}
	nodes = append(nodes,
		Node{ID: "snap1", Kind: NodeKindSnapshot, Payload: `{"state":"ok"}`, ObservedAt: t0},
		Node{ID: "agent1", Kind: NodeKindAgent, Payload: `{"summary":"triage done"}`, ObservedAt: t0.Add(time.Millisecond)},
	)

	b := BuildBundle(bundleRef(), nodes, nil)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxBundleBytes {
		t.Fatalf("bundle %d bytes exceeds %d", len(raw), MaxBundleBytes)
	}

	kept := map[string]bool{}
	for _, n := range b.Nodes {
		kept[n.ID] = true
	}
	if !kept["snap1"] || !kept["agent1"] {
		t.Fatalf("priority evidence dropped: %v", kept)
	}
	if !b.Truncated {
		t.Fatal("expected Truncated=true for an over-budget bundle")
	}
}

func TestBundleRedactsSecrets(t *testing.T) {
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	ref := bundleRef()
	nodes := []Node{
		{
			ID: "n1", Kind: NodeKindLog, ObservedAt: t0,
			Payload: "level=error token=eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnop namespace=default",
		},
		{
			ID: "n2", Kind: NodeKindLog, ObservedAt: t0.Add(time.Second),
			Payload: "authorization: Bearer eyJraWQiOiJrZXktaWQiLCJhbGciOiJSUzI1NiJ9.payload.more",
		},
		{
			ID: "n3", Kind: NodeKindLog, ObservedAt: t0.Add(2 * time.Second),
			Payload: "key material: -----BEGIN PRIVATE KEY-----\nMIIBEXAMPLEKEYDATA\n-----END PRIVATE KEY-----",
		},
	}

	b := BuildBundle(ref, nodes, nil)
	if len(b.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(b.Nodes))
	}
	for _, n := range b.Nodes {
		if strings.Contains(n.Payload, "eyJhbGciOiJIUzI1NiJ9") ||
			strings.Contains(n.Payload, "eyJraWQiOiJrZXktaWQi") ||
			strings.Contains(n.Payload, "MIIBEXAMPLEKEYDATA") {
			t.Fatalf("secret leaked in node %s: %s", n.ID, n.Payload)
		}
		if !strings.Contains(n.Payload, "[REDACTED]") {
			t.Fatalf("no redaction marker in node %s: %s", n.ID, n.Payload)
		}
	}
}

func TestRedactSecretsKeyValue(t *testing.T) {
	got := RedactSecrets(`export API_KEY=supersecret123 DATABASE_URL=postgres://x`)
	if strings.Contains(got, "supersecret123") {
		t.Fatalf("env value leaked: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("no redaction marker: %s", got)
	}
}

func TestRedactSecretsBearer(t *testing.T) {
	got := RedactSecrets(`Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnop body`)
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("bearer token leaked: %s", got)
	}
}

func FuzzBuildBundle(f *testing.F) {
	f.Add("")
	f.Add("token=abc123 password=hunter2")
	f.Add(strings.Repeat("x", MaxLogPayloadBytes*3))
	f.Add(`{"a":1,"b":[1,2,3],"nested":{"k":"v"}}`)

	f.Fuzz(func(t *testing.T, payload string) {
		nodes := []Node{{ID: "n1", Kind: NodeKindLog, Payload: payload}}
		b := BuildBundle(IncidentReference{ID: "inc-fuzz"}, nodes, nil)
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > MaxBundleBytes {
			t.Fatalf("bundle %d bytes exceeds %d", len(raw), MaxBundleBytes)
		}
	})
}
