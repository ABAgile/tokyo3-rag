package retrieval

import (
	"context"
	"fmt"

	"github.com/abagile/tokyo3-rag/internal/embed"
	"github.com/abagile/tokyo3-rag/internal/store"
)

// SearchDocsDense embeds query and returns ranked doc chunks.
func SearchDocsDense(ctx context.Context, st store.Store, embedder embed.Provider, query string, limit int) ([]Result, error) {
	vecs, err := embedder.EmbedTexts(ctx, []string{query}, embed.InputTypeQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	chunks, scores, err := st.SearchDocDense(ctx, vecs[0], limit)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(chunks))
	for i, c := range chunks {
		results[i] = Result{Score: scores[i], DocChunk: c}
	}
	return results, nil
}

// SearchCodeDense embeds query and returns ranked code nodes.
func SearchCodeDense(ctx context.Context, st store.Store, embedder embed.Provider, query string, limit int) ([]Result, error) {
	vecs, err := embedder.EmbedTexts(ctx, []string{query}, embed.InputTypeCodeQuery)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	nodes, scores, err := st.SearchCodeDense(ctx, vecs[0], limit)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(nodes))
	for i, n := range nodes {
		results[i] = Result{Score: scores[i], CodeNode: n}
	}
	return results, nil
}
