package retrieval

import (
	"context"

	"github.com/abagile/tokyo3-rag/internal/store"
)

// ExpandCodeGraph takes top-K code entry-point results and expands each via BFS,
// returning the union of entry points and their neighbors.
// Neighbor nodes are scored at entryScore * 0.9^hop_distance.
func ExpandCodeGraph(ctx context.Context, st store.Store, entryPoints []Result, depth int, edgeTypes []string) ([]Result, error) {
	seen := map[string]bool{}
	var expanded []Result

	for _, ep := range entryPoints {
		if ep.CodeNode == nil {
			continue
		}
		if seen[ep.CodeNode.ID] {
			continue
		}
		seen[ep.CodeNode.ID] = true
		expanded = append(expanded, ep)

		neighbors, err := st.GetCodeNeighbors(ctx, ep.CodeNode.ID, edgeTypes, depth)
		if err != nil {
			return nil, err
		}
		for _, n := range neighbors {
			if seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			// Graph neighbors score at 90% of the entry-point score.
			// Actual hop distance is not returned by the SQL query.
			expanded = append(expanded, Result{
				Score:    ep.Score * 0.9,
				CodeNode: n,
			})
		}
	}
	return expanded, nil
}
