package store

import (
	"context"
	"errors"

	"github.com/abagile/tokyo3-rag/internal/model"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store interface {
	// Documents
	UpsertDocChunk(ctx context.Context, c *model.DocChunk) error
	DeleteDocChunksBySource(ctx context.Context, sourcePath string) error
	SearchDocDense(ctx context.Context, embedding []float32, limit int) ([]*model.DocChunk, []float64, error)
	SearchDocSparse(ctx context.Context, query string, limit int) ([]*model.DocChunk, []float64, error)

	// Code nodes
	UpsertCodeNode(ctx context.Context, n *model.CodeNode) error
	DeleteCodeNodesByRepo(ctx context.Context, repoPath string) error
	SearchCodeDense(ctx context.Context, embedding []float32, limit int) ([]*model.CodeNode, []float64, error)
	GetCodeNeighbors(ctx context.Context, nodeID string, edgeTypes []string, depth int) ([]*model.CodeNode, error)

	// Code edges
	UpsertCodeEdge(ctx context.Context, e *model.CodeEdge) error

	// Ingest tracking
	UpsertIngestJob(ctx context.Context, j *model.IngestJob) error
	GetIngestJobByPath(ctx context.Context, sourcePath string) (*model.IngestJob, error)
	ListIngestJobs(ctx context.Context) ([]*model.IngestJob, error)

	Close() error
}
