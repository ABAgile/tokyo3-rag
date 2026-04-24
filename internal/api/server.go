// Package api implements the RAG HTTP API.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/abagile/tokyo3-rag/internal/embed"
	"github.com/abagile/tokyo3-rag/internal/generate"
	"github.com/abagile/tokyo3-rag/internal/store"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	store       store.Store
	embedder    embed.Provider
	generator   generate.Generator
	log         *slog.Logger
	token       string // static bearer token
	graphDepth  int
	maxCtxRunes int
}

// New creates a configured Server.
func New(st store.Store, embedder embed.Provider, gen generate.Generator, log *slog.Logger, token string, graphDepth, maxCtxRunes int) *Server {
	return &Server{
		store:       st,
		embedder:    embedder,
		generator:   gen,
		log:         log,
		token:       token,
		graphDepth:  graphDepth,
		maxCtxRunes: maxCtxRunes,
	}
}

// Routes registers all API routes and returns the handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/query", s.auth(s.handleQuery))
	mux.HandleFunc("POST /api/v1/ingest", s.auth(s.handleIngest))
	mux.HandleFunc("GET /api/v1/ingest/jobs", s.auth(s.handleListJobs))
	mux.HandleFunc("GET /api/v1/ingest/jobs/{id}", s.auth(s.handleGetJob))
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", s.auth(s.handleDeleteNode))

	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if bearer != s.token {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
