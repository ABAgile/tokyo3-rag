package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/abagile/tokyo3-rag/internal/model"
)

func (db *DB) UpsertDocChunk(ctx context.Context, c *model.DocChunk) error {
	meta, err := json.Marshal(c.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO doc_chunks
			(id, source_path, source_type, chunk_index, content, token_count,
			 embedding, ts_content, metadata, indexed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, to_tsvector('english', $5), $8, NOW())
		ON CONFLICT (source_path, chunk_index) DO UPDATE SET
			content     = EXCLUDED.content,
			token_count = EXCLUDED.token_count,
			embedding   = EXCLUDED.embedding,
			ts_content  = EXCLUDED.ts_content,
			metadata    = EXCLUDED.metadata,
			indexed_at  = EXCLUDED.indexed_at
	`, c.ID, c.SourcePath, c.SourceType, c.ChunkIndex, c.Content, c.TokenCount,
		pgvector.NewVector(c.Embedding), meta)
	return err
}

func (db *DB) DeleteDocChunksBySource(ctx context.Context, sourcePath string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM doc_chunks WHERE source_path = $1`, sourcePath)
	return err
}

func (db *DB) SearchDocDense(ctx context.Context, embedding []float32, limit int) ([]*model.DocChunk, []float64, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, source_path, source_type, chunk_index, content, token_count,
		       metadata, indexed_at,
		       1 - (embedding <=> $1) AS score
		FROM   doc_chunks
		WHERE  embedding IS NOT NULL
		ORDER  BY embedding <=> $1
		LIMIT  $2
	`, pgvector.NewVector(embedding), limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanDocChunks(rows)
}

func (db *DB) SearchDocSparse(ctx context.Context, query string, limit int) ([]*model.DocChunk, []float64, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, source_path, source_type, chunk_index, content, token_count,
		       metadata, indexed_at,
		       ts_rank_cd(ts_content, websearch_to_tsquery('english', $1)) AS score
		FROM   doc_chunks
		WHERE  ts_content @@ websearch_to_tsquery('english', $1)
		ORDER  BY score DESC
		LIMIT  $2
	`, query, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanDocChunks(rows)
}

func scanDocChunks(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*model.DocChunk, []float64, error) {
	var chunks []*model.DocChunk
	var scores []float64
	for rows.Next() {
		var c model.DocChunk
		var metaRaw []byte
		var score float64
		var indexedAt time.Time
		if err := rows.Scan(
			&c.ID, &c.SourcePath, &c.SourceType, &c.ChunkIndex,
			&c.Content, &c.TokenCount, &metaRaw, &indexedAt, &score,
		); err != nil {
			return nil, nil, err
		}
		c.IndexedAt = indexedAt
		if metaRaw != nil {
			_ = json.Unmarshal(metaRaw, &c.Metadata)
		}
		chunks = append(chunks, &c)
		scores = append(scores, score)
	}
	return chunks, scores, rows.Err()
}
