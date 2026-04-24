package postgres

import (
	"context"
	"time"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/abagile/tokyo3-rag/internal/model"
)

func (db *DB) UpsertCodeNode(ctx context.Context, n *model.CodeNode) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO code_nodes
			(id, repo_path, language, node_type, name, qualified,
			 content, line_start, line_end, embedding, indexed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (repo_path, language, qualified) DO UPDATE SET
			node_type  = EXCLUDED.node_type,
			name       = EXCLUDED.name,
			content    = EXCLUDED.content,
			line_start = EXCLUDED.line_start,
			line_end   = EXCLUDED.line_end,
			embedding  = EXCLUDED.embedding,
			indexed_at = EXCLUDED.indexed_at
	`, n.ID, n.RepoPath, n.Language, n.NodeType, n.Name, n.Qualified,
		n.Content, n.LineStart, n.LineEnd, pgvector.NewVector(n.Embedding))
	return err
}

func (db *DB) DeleteCodeNodesByRepo(ctx context.Context, repoPath string) error {
	// edges cascade via ON DELETE CASCADE on code_nodes
	_, err := db.pool.Exec(ctx, `DELETE FROM code_nodes WHERE repo_path = $1`, repoPath)
	return err
}

func (db *DB) SearchCodeDense(ctx context.Context, embedding []float32, limit int) ([]*model.CodeNode, []float64, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, repo_path, language, node_type, name, qualified,
		       content, line_start, line_end, indexed_at,
		       1 - (embedding <=> $1) AS score
		FROM   code_nodes
		WHERE  embedding IS NOT NULL
		ORDER  BY embedding <=> $1
		LIMIT  $2
	`, pgvector.NewVector(embedding), limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var nodes []*model.CodeNode
	var scores []float64
	for rows.Next() {
		var n model.CodeNode
		var indexedAt time.Time
		var score float64
		if err := rows.Scan(
			&n.ID, &n.RepoPath, &n.Language, &n.NodeType, &n.Name,
			&n.Qualified, &n.Content, &n.LineStart, &n.LineEnd, &indexedAt, &score,
		); err != nil {
			return nil, nil, err
		}
		n.IndexedAt = indexedAt
		nodes = append(nodes, &n)
		scores = append(scores, score)
	}
	return nodes, scores, rows.Err()
}

// GetCodeNeighbors returns nodes reachable from nodeID within depth hops,
// traversing only edges of the given types (nil = all types).
func (db *DB) GetCodeNeighbors(ctx context.Context, nodeID string, edgeTypes []string, depth int) ([]*model.CodeNode, error) {
	var q string
	var args []any
	if len(edgeTypes) > 0 {
		q = `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT to_node_id, 1
				FROM   code_edges
				WHERE  from_node_id = $1 AND edge_type = ANY($3::text[])
				UNION
				SELECT e.to_node_id, r.depth + 1
				FROM   code_edges e
				JOIN   reachable r ON e.from_node_id = r.id
				WHERE  r.depth < $2 AND e.edge_type = ANY($3::text[])
			)
			SELECT DISTINCT n.id, n.repo_path, n.language, n.node_type, n.name,
			       n.qualified, n.content, n.line_start, n.line_end, n.indexed_at
			FROM   code_nodes n
			JOIN   reachable r ON n.id = r.id
		`
		args = []any{nodeID, depth, edgeTypes}
	} else {
		q = `
			WITH RECURSIVE reachable(id, depth) AS (
				SELECT to_node_id, 1
				FROM   code_edges
				WHERE  from_node_id = $1
				UNION
				SELECT e.to_node_id, r.depth + 1
				FROM   code_edges e
				JOIN   reachable r ON e.from_node_id = r.id
				WHERE  r.depth < $2
			)
			SELECT DISTINCT n.id, n.repo_path, n.language, n.node_type, n.name,
			       n.qualified, n.content, n.line_start, n.line_end, n.indexed_at
			FROM   code_nodes n
			JOIN   reachable r ON n.id = r.id
		`
		args = []any{nodeID, depth}
	}

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*model.CodeNode
	for rows.Next() {
		var n model.CodeNode
		var indexedAt time.Time
		if err := rows.Scan(
			&n.ID, &n.RepoPath, &n.Language, &n.NodeType, &n.Name,
			&n.Qualified, &n.Content, &n.LineStart, &n.LineEnd, &indexedAt,
		); err != nil {
			return nil, err
		}
		n.IndexedAt = indexedAt
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

func (db *DB) UpsertCodeEdge(ctx context.Context, e *model.CodeEdge) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO code_edges (id, from_node_id, to_node_id, edge_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (from_node_id, to_node_id, edge_type) DO NOTHING
	`, e.ID, e.FromNodeID, e.ToNodeID, e.EdgeType)
	if isUnique(err) {
		return nil
	}
	return err
}
