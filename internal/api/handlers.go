package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/google/uuid"

	"github.com/abagile/tokyo3-rag/internal/ingest"
	codeingest "github.com/abagile/tokyo3-rag/internal/ingest/code"
	"github.com/abagile/tokyo3-rag/internal/retrieval"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleQuery is the main RAG endpoint. Streams SSE tokens to the client.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query     string `json:"query"`
		QueryType string `json:"query_type"` // "doc" | "code" | "both" (default)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	ctx := r.Context()
	const topK = 10

	qt := retrieval.ClassifyQuery(req.Query)
	switch req.QueryType {
	case "doc":
		qt = retrieval.QueryTypeDoc
	case "code":
		qt = retrieval.QueryTypeCode
	case "both":
		qt = retrieval.QueryTypeBoth
	}

	var docResults, codeResults []retrieval.Result

	if qt == retrieval.QueryTypeDoc || qt == retrieval.QueryTypeBoth {
		dense, err := retrieval.SearchDocsDense(ctx, s.store, s.embedder, req.Query, topK)
		if err != nil {
			s.log.Error("doc dense search", "err", err)
		}
		sparse, err := retrieval.SearchDocsSparse(ctx, s.store, req.Query, topK)
		if err != nil {
			s.log.Error("doc sparse search", "err", err)
		}
		docResults = retrieval.RRF(dense, sparse)
	}

	if qt == retrieval.QueryTypeCode || qt == retrieval.QueryTypeBoth {
		dense, err := retrieval.SearchCodeDense(ctx, s.store, s.embedder, req.Query, topK)
		if err != nil {
			s.log.Error("code dense search", "err", err)
		}
		expanded, err := retrieval.ExpandCodeGraph(ctx, s.store, dense, s.graphDepth, nil)
		if err != nil {
			s.log.Error("graph expand", "err", err)
			expanded = dense
		}
		codeResults = expanded
	}

	merged := retrieval.RRF(docResults, codeResults)

	// Stream SSE response.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)

	writeSSE := func(data string) {
		fmt.Fprintf(w, "data: %s\n\n", data)
		if canFlush {
			flusher.Flush()
		}
	}

	tokens := s.generator.Stream(ctx, req.Query, merged, s.maxCtxRunes)
	for tok := range tokens {
		if tok.Error != nil {
			s.log.Error("generate stream", "err", tok.Error)
			break
		}
		if tok.Done {
			break
		}
		b, _ := json.Marshal(map[string]string{"type": "token", "content": tok.Text})
		writeSSE(string(b))
	}

	// Emit sources.
	type source struct {
		Path      string `json:"path"`
		LineStart int    `json:"line_start,omitempty"`
		LineEnd   int    `json:"line_end,omitempty"`
		ChunkIdx  int    `json:"chunk_index,omitempty"`
	}
	var sources []source
	for _, r := range merged {
		if r.DocChunk != nil {
			sources = append(sources, source{Path: r.DocChunk.SourcePath, ChunkIdx: r.DocChunk.ChunkIndex})
		} else if r.CodeNode != nil {
			sources = append(sources, source{Path: r.CodeNode.RepoPath, LineStart: r.CodeNode.LineStart, LineEnd: r.CodeNode.LineEnd})
		}
	}
	b, _ := json.Marshal(map[string]any{"type": "sources", "sources": sources})
	writeSSE(string(b))
	writeSSE(`{"type":"done"}`)
}

// handleIngest queues a file or directory for ingestion.
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path       string `json:"path"`
		SourceType string `json:"source_type"` // "code" | "pdf" | "markdown"
		Language   string `json:"language"`    // "go" | "ruby" | "clojure" (code only)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "path and source_type are required")
		return
	}

	jobID := uuid.New().String()

	go func() {
		ctx := context.Background()
		switch req.SourceType {
		case "code":
			p := codeingest.New(s.store, s.embedder, runtime.GOMAXPROCS(0))
			if err := p.IngestDir(ctx, req.Path, req.Language); err != nil {
				s.log.Error("code ingest", "path", req.Path, "err", err)
			}
		case "pdf", "markdown":
			p := ingest.NewDocPipeline(s.store, s.embedder)
			info, err := os.Stat(req.Path)
			if err != nil {
				s.log.Error("stat path", "path", req.Path, "err", err)
				return
			}
			if info.IsDir() {
				entries, _ := os.ReadDir(req.Path)
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					_ = p.IngestFile(ctx, filepath.Join(req.Path, e.Name()), req.SourceType)
				}
			} else {
				_ = p.IngestFile(ctx, req.Path, req.SourceType)
			}
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "accepted"})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListIngestJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobs, err := s.store.ListIngestJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, j := range jobs {
		if j.ID == id {
			writeJSON(w, http.StatusOK, j)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	// Node deletion cascades to edges via FK. Implemented via direct repo delete
	// scoped to the node's repo_path; full per-node delete is a future extension.
	writeError(w, http.StatusNotImplemented, "per-node deletion not yet implemented; use DELETE /api/v1/ingest?repo=<path> to remove all nodes for a repo")
}
