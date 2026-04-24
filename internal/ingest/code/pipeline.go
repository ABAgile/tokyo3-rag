// Package code provides the code ingestion pipeline.
package code

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/abagile/tokyo3-rag/internal/embed"
	"github.com/abagile/tokyo3-rag/internal/ingest/code/parser"
	"github.com/abagile/tokyo3-rag/internal/model"
	"github.com/abagile/tokyo3-rag/internal/store"
)

// Pipeline walks a directory, parses source files, embeds nodes, and stores them.
type Pipeline struct {
	store    store.Store
	embedder embed.Provider
	workers  int
}

// New creates a code ingestion pipeline.
func New(st store.Store, embedder embed.Provider, workers int) *Pipeline {
	if workers <= 0 {
		workers = 4
	}
	return &Pipeline{store: st, embedder: embedder, workers: workers}
}

// IngestDir walks root, ingesting all recognised source files.
// language must be "go", "ruby", or "clojure" (empty = auto-detect by extension).
func (p *Pipeline) IngestDir(ctx context.Context, root, language string) error {
	type job struct {
		path     string
		language string
	}

	jobs := make(chan job, 64)
	var wg sync.WaitGroup
	errs := make(chan error, p.workers)

	// Start workers.
	for range p.workers {
		wg.Go(func() {
			for j := range jobs {
				if err := p.ingestFile(ctx, root, j.path, j.language); err != nil {
					select {
					case errs <- fmt.Errorf("%s: %w", j.path, err):
					default:
					}
				}
			}
		})
	}

	// Walk and dispatch.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		lang := language
		if lang == "" {
			lang = detectLanguage(path)
		}
		if lang == "" {
			return nil
		}
		select {
		case jobs <- job{path: path, language: lang}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	close(jobs)
	wg.Wait()
	close(errs)

	var firstErr error
	for e := range errs {
		if firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

func (p *Pipeline) ingestFile(ctx context.Context, root, path, language string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	relPath, _ := filepath.Rel(root, path)

	// Skip unchanged files.
	if job, err := p.store.GetIngestJobByPath(ctx, relPath); err == nil &&
		job.Status == "done" && job.FileHash == hash {
		return nil
	}

	job := &model.IngestJob{
		ID:         uuid.New().String(),
		SourcePath: relPath,
		SourceType: "code",
		Language:   language,
		FileHash:   hash,
		Status:     "pending",
	}
	_ = p.store.UpsertIngestJob(ctx, job)

	var nodes []*model.CodeNode
	var edges []*model.CodeEdge

	switch language {
	case "go":
		nodes, edges, err = parser.ParseGo(relPath, content)
	case "ruby":
		nodes, edges, err = parser.ParseRuby(relPath, content)
	case "clojure":
		nodes, edges, err = parser.ParseClojure(relPath, content)
	default:
		return nil
	}
	if err != nil {
		job.Status = "error"
		job.ErrorMsg = err.Error()
		_ = p.store.UpsertIngestJob(ctx, job)
		return err
	}
	if len(nodes) == 0 {
		job.Status = "done"
		_ = p.store.UpsertIngestJob(ctx, job)
		return nil
	}

	// Embed all nodes in one batch.
	texts := make([]string, len(nodes))
	for i, n := range nodes {
		texts[i] = n.Content
	}
	embeddings, err := p.embedder.EmbedTexts(ctx, texts, embed.InputTypeCode)
	if err != nil {
		job.Status = "error"
		job.ErrorMsg = err.Error()
		_ = p.store.UpsertIngestJob(ctx, job)
		return fmt.Errorf("embed: %w", err)
	}
	for i, n := range nodes {
		if i < len(embeddings) {
			n.Embedding = embeddings[i]
		}
		if err := p.store.UpsertCodeNode(ctx, n); err != nil {
			return fmt.Errorf("upsert node: %w", err)
		}
	}
	for _, e := range edges {
		_ = p.store.UpsertCodeEdge(ctx, e)
	}

	job.Status = "done"
	_ = p.store.UpsertIngestJob(ctx, job)
	return nil
}

func detectLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".rb", ".rake", ".gemspec":
		return "ruby"
	case ".clj", ".cljs", ".cljc", ".edn":
		return "clojure"
	}
	return ""
}
