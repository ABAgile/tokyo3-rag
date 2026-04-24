// Package ingest provides document ingestion pipelines.
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"github.com/yuin/goldmark"

	"github.com/abagile/tokyo3-rag/internal/embed"
	"github.com/abagile/tokyo3-rag/internal/model"
	"github.com/abagile/tokyo3-rag/internal/store"
)

const (
	chunkSize    = 2048 // ~512 tokens at 4 chars/token
	chunkOverlap = 256  // ~64-token overlap
)

// DocPipeline ingests PDF and markdown files into the document store.
type DocPipeline struct {
	store    store.Store
	embedder embed.Provider
}

// NewDocPipeline creates a document ingestion pipeline.
func NewDocPipeline(st store.Store, embedder embed.Provider) *DocPipeline {
	return &DocPipeline{store: st, embedder: embedder}
}

// IngestFile ingests a single file. sourceType must be "pdf" or "markdown".
func (p *DocPipeline) IngestFile(ctx context.Context, path, sourceType string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	if job, err := p.store.GetIngestJobByPath(ctx, path); err == nil &&
		job.Status == "done" && job.FileHash == hash {
		return nil
	}

	job := &model.IngestJob{
		ID:         uuid.New().String(),
		SourcePath: path,
		SourceType: "doc",
		FileHash:   hash,
		Status:     "pending",
	}
	_ = p.store.UpsertIngestJob(ctx, job)

	var text string
	switch sourceType {
	case "pdf":
		text, err = extractPDF(path)
	case "markdown":
		text = extractMarkdown(content)
	default:
		text = string(content)
	}
	if err != nil {
		job.Status = "error"
		job.ErrorMsg = err.Error()
		_ = p.store.UpsertIngestJob(ctx, job)
		return err
	}

	chunks := chunk(text, chunkSize, chunkOverlap)
	if len(chunks) == 0 {
		job.Status = "done"
		_ = p.store.UpsertIngestJob(ctx, job)
		return nil
	}

	// Embed all chunks.
	texts := make([]string, len(chunks))
	copy(texts, chunks)
	embeddings, err := p.embedder.EmbedTexts(ctx, texts, embed.InputTypeDocument)
	if err != nil {
		job.Status = "error"
		job.ErrorMsg = err.Error()
		_ = p.store.UpsertIngestJob(ctx, job)
		return fmt.Errorf("embed: %w", err)
	}

	// Delete old chunks for this source before upserting new ones.
	_ = p.store.DeleteDocChunksBySource(ctx, path)

	for i, c := range chunks {
		dc := &model.DocChunk{
			ID:         uuid.New().String(),
			SourcePath: path,
			SourceType: sourceType,
			ChunkIndex: i,
			Content:    c,
			TokenCount: len(c) / 4,
		}
		if i < len(embeddings) {
			dc.Embedding = embeddings[i]
		}
		if err := p.store.UpsertDocChunk(ctx, dc); err != nil {
			return fmt.Errorf("upsert chunk %d: %w", i, err)
		}
	}

	job.Status = "done"
	_ = p.store.UpsertIngestJob(ctx, job)
	return nil
}

func extractPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(text)
		sb.WriteRune('\n')
	}
	return sb.String(), nil
}

func extractMarkdown(content []byte) string {
	var buf bytes.Buffer
	if err := goldmark.Convert(content, &buf); err != nil {
		return string(content)
	}
	// Strip HTML tags from goldmark output.
	s := buf.String()
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// chunk splits text into overlapping windows of approximately size runes.
func chunk(text string, size, overlap int) []string {
	runes := []rune(text)
	if len(runes) <= size {
		if len(strings.TrimSpace(text)) == 0 {
			return nil
		}
		return []string{text}
	}

	var chunks []string
	step := size - overlap
	if step <= 0 {
		step = size / 2
	}
	for i := 0; i < len(runes); i += step {
		end := min(i+size, len(runes))
		c := strings.TrimSpace(string(runes[i:end]))
		if c != "" {
			chunks = append(chunks, c)
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}
