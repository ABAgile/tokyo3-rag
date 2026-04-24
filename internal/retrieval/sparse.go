package retrieval

import (
	"context"

	"github.com/abagile/tokyo3-rag/internal/store"
)

// SearchDocsSparse returns ranked doc chunks via PostgreSQL FTS.
func SearchDocsSparse(ctx context.Context, st store.Store, query string, limit int) ([]Result, error) {
	chunks, scores, err := st.SearchDocSparse(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(chunks))
	for i, c := range chunks {
		results[i] = Result{Score: scores[i], DocChunk: c}
	}
	return results, nil
}
