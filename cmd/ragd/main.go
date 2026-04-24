// ragd is the RAG knowledge service.
//
// Required environment variables:
//
//	RAG_DATABASE_URL     PostgreSQL DSN with pgvector extension installed
//	LLM_API_KEY          API key for the selected LLM provider
//
// Embedding provider (EMBED_PROVIDER, default: voyage):
//
//	EMBED_PROVIDER       "voyage" or "ollama"
//
//	voyage:
//	  VOYAGE_API_KEY     Voyage AI API key (required when EMBED_PROVIDER=voyage)
//
//	ollama:
//	  OLLAMA_BASE_URL    Ollama server URL (default: http://localhost:11434)
//	  OLLAMA_DOC_MODEL   Model for documents/queries (default: nomic-embed-text)
//	  OLLAMA_CODE_MODEL  Model for code (default: bge-m3)
//
// LLM provider (LLM_PROVIDER, default: anthropic):
//
//	LLM_PROVIDER         anthropic | openai | gemini | ollama
//	LLM_MODEL            Model override (provider-specific default if omitted)
//	                       anthropic default: claude-sonnet-4-6
//	                       openai default:    gpt-4o
//	                       gemini default:    gemini-2.0-flash
//	                       ollama default:    gemma3
//
//	ollama LLM shares OLLAMA_BASE_URL with the embed provider (default: http://localhost:11434).
//	LLM_API_KEY is ignored when LLM_PROVIDER=ollama.
//
// Other optional:
//
//	RAG_API_TOKEN        Static bearer token for API auth (empty = no auth, dev only)
//	RAG_ADDR             Listen address (default: :8080)
//	RAG_GRAPH_DEPTH      BFS depth for code graph expansion (default: 2)
//	RAG_CONTEXT_RUNES    Max runes of context assembled for generation (default: 400000)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abagile/tokyo3-rag/internal/api"
	"github.com/abagile/tokyo3-rag/internal/embed"
	"github.com/abagile/tokyo3-rag/internal/generate"
	"github.com/abagile/tokyo3-rag/internal/store/postgres"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, log); err != nil {
		fmt.Fprintf(os.Stderr, "ragd: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	dsn := mustEnv("RAG_DATABASE_URL")

	embedProvider := envOr("EMBED_PROVIDER", "voyage")

	provider := envOr("LLM_PROVIDER", "anthropic")
	llmModel := os.Getenv("LLM_MODEL")
	ollamaBase := envOr("OLLAMA_BASE_URL", "http://localhost:11434")

	var llmKey string
	if strings.ToLower(provider) == "ollama" {
		llmKey = ollamaBase
	} else {
		llmKey = mustEnv("LLM_API_KEY")
	}

	addr := envOr("RAG_ADDR", ":8080")
	apiToken := os.Getenv("RAG_API_TOKEN")
	graphDepth := envInt("RAG_GRAPH_DEPTH", 2)
	maxCtxRunes := envInt("RAG_CONTEXT_RUNES", 400_000)

	log.Info("connecting to database")
	db, err := postgres.Open(dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	log.Info("database ready")

	gen, err := generate.New(provider, llmKey, llmModel)
	if err != nil {
		return fmt.Errorf("LLM provider: %w", err)
	}
	log.Info("LLM provider ready", "provider", provider)

	embedder, err := newEmbedder(embedProvider)
	if err != nil {
		return fmt.Errorf("embed provider: %w", err)
	}
	log.Info("embed provider ready", "provider", embedProvider)

	srv := api.New(db, embedder, gen, log, apiToken, graphDepth, maxCtxRunes)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		return httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func newEmbedder(provider string) (embed.Provider, error) {
	switch provider {
	case "voyage":
		return embed.NewVoyage(mustEnv("VOYAGE_API_KEY")), nil
	case "ollama":
		baseURL := os.Getenv("OLLAMA_BASE_URL")
		docModel := envOr("OLLAMA_DOC_MODEL", "nomic-embed-text")
		codeModel := envOr("OLLAMA_CODE_MODEL", "bge-m3")
		return embed.NewOllama(docModel, codeModel, baseURL), nil
	default:
		return nil, fmt.Errorf("unknown EMBED_PROVIDER %q: must be voyage or ollama", provider)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "ragd: %s is required\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
