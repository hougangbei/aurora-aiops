package evidence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func newEvidenceRepository(t *testing.T) *sqlRepository {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &sqlRepository{db: db}
}

// mustInsertIncident seeds the parent incidents row so the FK cascade chain
// is exercised; evidence tests stay decoupled from the aiops package.
func mustInsertIncident(t *testing.T, repo *sqlRepository, incidentID string) {
	t.Helper()
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err := repo.db.Exec(`
		INSERT OR IGNORE INTO incidents (id, summary, severity, status, namespace, resource_kind, resource_name, created_at, updated_at)
		VALUES (?, 'test incident', 'info', 'received', 'default', 'Pod', 'pod-0', ?, ?)`,
		incidentID, now, now,
	)
	if err != nil {
		t.Fatalf("insert incident: %v", err)
	}
}

func mustInsertNodes(t *testing.T, repo *sqlRepository, incidentID string, ids ...string) {
	t.Helper()
	mustInsertIncident(t, repo, incidentID)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	for i, id := range ids {
		if err := repo.AddNode(ctx, Node{
			IncidentID: incidentID,
			ID:         id,
			Kind:       NodeKindLog,
			Payload:    "payload-" + id,
			ObservedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("insert node %s: %v", id, err)
		}
	}
}

func mustAddEdge(t *testing.T, repo *sqlRepository, edge Edge) {
	t.Helper()
	if err := repo.AddEdge(context.Background(), edge); err != nil {
		t.Fatalf("add edge: %v", err)
	}
}

func TestRepositoryInsertAndGetNode(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertIncident(t, repo, "inc-1")

	node := Node{
		IncidentID: "inc-1",
		ID:         "n1",
		Kind:       NodeKindLog,
		Payload:    "  {\"msg\": \"boom\", \"severity\": \"error\"}  ",
		ObservedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
	if err := repo.AddNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetNode(context.Background(), "inc-1", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash == "" {
		t.Fatal("hash was not computed at insert")
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at was not filled at insert")
	}
	// Original payload is stored verbatim; the hash covers the normalized form.
	if got.Payload != node.Payload {
		t.Fatalf("payload=%q want=%q", got.Payload, node.Payload)
	}
}

func TestRepositoryGetNodeNotFound(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertIncident(t, repo, "inc-1")
	_, err := repo.GetNode(context.Background(), "inc-1", "missing")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("got err=%v, want ErrNodeNotFound", err)
	}
}

func TestRepositoryStableHash(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertIncident(t, repo, "inc-1")
	ctx := context.Background()

	// Same logical payload: different whitespace, quoting, and object key order.
	a := Node{IncidentID: "inc-1", ID: "a", Kind: NodeKindLog, Payload: `{"msg":"boom","severity":"error"}`}
	b := Node{IncidentID: "inc-1", ID: "b", Kind: NodeKindLog, Payload: `  { "severity" : "error" , "msg" : "boom" }  `}
	if err := repo.AddNode(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddNode(ctx, b); err != nil {
		t.Fatal(err)
	}

	ga, err := repo.GetNode(ctx, "inc-1", "a")
	if err != nil {
		t.Fatal(err)
	}
	gb, err := repo.GetNode(ctx, "inc-1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if ga.Hash != gb.Hash {
		t.Fatalf("stable hash mismatch: %s != %s", ga.Hash, gb.Hash)
	}
	if len(ga.Hash) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %q", ga.Hash)
	}
}

func TestRepositoryRejectsSelfLoop(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertNodes(t, repo, "inc-1", "a")
	err := repo.AddEdge(context.Background(), Edge{
		IncidentID: "inc-1", FromID: "a", ToID: "a", Relation: RelationSupports,
	})
	if !errors.Is(err, ErrEvidenceCycle) {
		t.Fatalf("got err=%v, want ErrEvidenceCycle", err)
	}
}

func TestRepositoryRejectsCycle(t *testing.T) {
	repo := newEvidenceRepository(t)
	ctx := context.Background()
	mustInsertNodes(t, repo, "inc-1", "a", "b", "c")
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "a", ToID: "b", Relation: RelationSupports})
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "b", ToID: "c", Relation: RelationSupports})
	err := repo.AddEdge(ctx, Edge{IncidentID: "inc-1", FromID: "c", ToID: "a", Relation: RelationSupports})
	if !errors.Is(err, ErrEvidenceCycle) {
		t.Fatalf("got err=%v, want ErrEvidenceCycle", err)
	}
}

func TestRepositoryRejectsBackEdge(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertNodes(t, repo, "inc-1", "a", "b")
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "a", ToID: "b", Relation: RelationSupports})
	// b -> a would close a two-node cycle.
	err := repo.AddEdge(context.Background(), Edge{
		IncidentID: "inc-1", FromID: "b", ToID: "a", Relation: RelationSupports,
	})
	if !errors.Is(err, ErrEvidenceCycle) {
		t.Fatalf("got err=%v, want ErrEvidenceCycle", err)
	}
}

func TestRepositoryAcceptsAcyclicChain(t *testing.T) {
	repo := newEvidenceRepository(t)
	ctx := context.Background()
	mustInsertNodes(t, repo, "inc-1", "a", "b", "c")
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "a", ToID: "b", Relation: RelationSupports})
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "b", ToID: "c", Relation: RelationSupports})
	// Cross edge a -> c stays acyclic.
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "a", ToID: "c", Relation: RelationContradicts})

	edges, err := repo.ListEdges(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 3 {
		t.Fatalf("got %d edges, want 3", len(edges))
	}
}

func TestRepositoryRejectsEdgeToMissingNode(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertIncident(t, repo, "inc-1")
	err := repo.AddEdge(context.Background(), Edge{
		IncidentID: "inc-1", FromID: "a", ToID: "b", Relation: RelationSupports,
	})
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("got err=%v, want ErrNodeNotFound", err)
	}
}

func TestRepositoryListNodesOrderedByObservedAt(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertNodes(t, repo, "inc-1", "c", "a", "b") // observed_at 0s, 1s, 2s
	nodes, err := repo.ListNodes(context.Background(), "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	want := []string{"c", "a", "b"} // ascending observed_at, id tiebreak
	for i, n := range nodes {
		if n.ID != want[i] {
			t.Fatalf("node[%d].ID=%q want %q", i, n.ID, want[i])
		}
	}
}

func TestRepositoryCascadeOnIncidentDelete(t *testing.T) {
	repo := newEvidenceRepository(t)
	mustInsertNodes(t, repo, "inc-1", "a", "b")
	mustAddEdge(t, repo, Edge{IncidentID: "inc-1", FromID: "a", ToID: "b", Relation: RelationSupports})

	if _, err := repo.db.Exec(`DELETE FROM incidents WHERE id = 'inc-1'`); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	nodes, err := repo.ListNodes(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	edges, err := repo.ListEdges(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("cascade failed: nodes=%d edges=%d", len(nodes), len(edges))
	}
}
