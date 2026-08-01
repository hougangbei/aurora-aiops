package evidence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrEvidenceCycle = errors.New("evidence edge would create a cycle")
	ErrNodeNotFound  = errors.New("evidence node not found")
	ErrNodeExists    = errors.New("evidence node already exists")
	ErrEdgeExists    = errors.New("evidence edge already exists")
)

// Repository persists the evidence DAG for incidents. Edges are only accepted
// when both endpoints exist and the resulting graph stays acyclic.
type Repository interface {
	AddNode(ctx context.Context, node Node) error
	GetNode(ctx context.Context, incidentID, nodeID string) (Node, error)
	ListNodes(ctx context.Context, incidentID string) ([]Node, error)
	AddEdge(ctx context.Context, edge Edge) error
	ListEdges(ctx context.Context, incidentID string) ([]Edge, error)
}

type sqlRepository struct {
	db *sql.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *sql.DB) Repository {
	return &sqlRepository{db: db}
}

func (r *sqlRepository) AddNode(ctx context.Context, node Node) error {
	if node.Hash == "" {
		node.Hash = StableHash(node.Payload)
	}
	createdAt := node.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	observedAt := node.ObservedAt
	if observedAt.IsZero() {
		observedAt = createdAt
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO evidence_nodes (incident_id, id, kind, payload, observed_at, hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		node.IncidentID, node.ID, string(node.Kind), node.Payload,
		observedAt.UTC().Format(time.RFC3339Nano), node.Hash,
		createdAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("node %s for incident %s: %w", node.ID, node.IncidentID, ErrNodeExists)
		}
		return fmt.Errorf("insert evidence node: %w", err)
	}
	return nil
}

func (r *sqlRepository) GetNode(ctx context.Context, incidentID, nodeID string) (Node, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT incident_id, id, kind, payload, observed_at, hash, created_at
		FROM evidence_nodes WHERE incident_id = ? AND id = ?`, incidentID, nodeID)
	return scanNode(row)
}

func (r *sqlRepository) ListNodes(ctx context.Context, incidentID string) ([]Node, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT incident_id, id, kind, payload, observed_at, hash, created_at
		FROM evidence_nodes WHERE incident_id = ? ORDER BY observed_at ASC, id ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("query evidence nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence nodes: %w", err)
	}
	return nodes, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(sc rowScanner) (Node, error) {
	var n Node
	var kind, observedAtStr, createdAtStr string
	if err := sc.Scan(&n.IncidentID, &n.ID, &kind, &n.Payload, &observedAtStr, &n.Hash, &createdAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNodeNotFound
		}
		return Node{}, fmt.Errorf("scan evidence node: %w", err)
	}
	n.Kind = NodeKind(kind)
	observedAt, err := time.Parse(time.RFC3339Nano, observedAtStr)
	if err != nil {
		return Node{}, fmt.Errorf("parse observed_at: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return Node{}, fmt.Errorf("parse created_at: %w", err)
	}
	n.ObservedAt = observedAt
	n.CreatedAt = createdAt
	return n, nil
}

func (r *sqlRepository) AddEdge(ctx context.Context, edge Edge) error {
	createdAt := edge.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	edge.CreatedAt = createdAt

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin edge tx: %w", err)
	}
	defer tx.Rollback()

	for _, id := range []string{edge.FromID, edge.ToID} {
		var one int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM evidence_nodes WHERE incident_id = ? AND id = ?`,
			edge.IncidentID, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNodeNotFound
		}
		if err != nil {
			return fmt.Errorf("check evidence node %s: %w", id, err)
		}
	}

	cycle, err := r.wouldCreateCycle(ctx, tx, edge)
	if err != nil {
		return fmt.Errorf("detect cycle: %w", err)
	}
	if cycle {
		return ErrEvidenceCycle
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO evidence_edges (incident_id, from_id, to_id, relation, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		edge.IncidentID, edge.FromID, edge.ToID, string(edge.Relation),
		edge.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrEdgeExists
		}
		return fmt.Errorf("insert evidence edge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit edge tx: %w", err)
	}
	return nil
}

// wouldCreateCycle reports whether adding edge.From->edge.To would form a
// cycle given the edges already committed to tx. A cycle exists exactly when a
// path already leads from edge.To back to edge.From; a self-loop is the
// degenerate case. Rows are fully drained before recursion so the single tx
// connection is never held open across nested queries.
func (r *sqlRepository) wouldCreateCycle(ctx context.Context, tx *sql.Tx, edge Edge) (bool, error) {
	visited := make(map[string]bool)
	var walk func(current string) (bool, error)
	walk = func(current string) (bool, error) {
		if current == edge.FromID {
			return true, nil
		}
		if visited[current] {
			return false, nil
		}
		visited[current] = true

		rows, err := tx.QueryContext(ctx, `
			SELECT to_id FROM evidence_edges WHERE incident_id = ? AND from_id = ?`,
			edge.IncidentID, current)
		if err != nil {
			return false, fmt.Errorf("query outgoing edges of %s: %w", current, err)
		}
		var nextIDs []string
		for rows.Next() {
			var next string
			if err := rows.Scan(&next); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan outgoing edge: %w", err)
			}
			nextIDs = append(nextIDs, next)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, fmt.Errorf("iterate outgoing edges of %s: %w", current, err)
		}
		if err := rows.Close(); err != nil {
			return false, fmt.Errorf("close outgoing edges: %w", err)
		}

		for _, next := range nextIDs {
			found, err := walk(next)
			if err != nil {
				return false, err
			}
			if found {
				return true, nil
			}
		}
		return false, nil
	}
	return walk(edge.ToID)
}

func (r *sqlRepository) ListEdges(ctx context.Context, incidentID string) ([]Edge, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT incident_id, from_id, to_id, relation, created_at
		FROM evidence_edges WHERE incident_id = ? ORDER BY created_at ASC, from_id ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("query evidence edges: %w", err)
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		var relation, createdAtStr string
		if err := rows.Scan(&e.IncidentID, &e.FromID, &e.ToID, &relation, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan evidence edge: %w", err)
		}
		e.Relation = Relation(relation)
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse edge created_at: %w", err)
		}
		e.CreatedAt = createdAt
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence edges: %w", err)
	}
	return edges, nil
}
