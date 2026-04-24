// Package generate provides streaming LLM generation for RAG responses.
// Supported providers: anthropic, openai, gemini, ollama.
package generate

import (
	"context"
	"fmt"
	"strings"

	"github.com/abagile/tokyo3-rag/internal/retrieval"
)

// Token is a single streamed event from any LLM provider.
type Token struct {
	Text  string
	Done  bool
	Error error
}

// Generator streams an answer to a query given retrieved context.
type Generator interface {
	Stream(ctx context.Context, query string, results []retrieval.Result, maxContextRunes int) <-chan Token
}

// New returns the Generator for the named provider.
// provider is case-insensitive: "anthropic", "openai", "gemini", or "ollama".
// model is optional — each provider has a sensible default.
// For ollama, apiKey is ignored and baseURL is read from the second positional
// argument via NewOllamaGenerator; pass an empty string to use localhost:11434.
func New(provider, apiKey, model string) (Generator, error) {
	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		return NewClaude(apiKey, model), nil
	case "openai":
		return NewOpenAI(apiKey, model), nil
	case "gemini":
		return NewGemini(apiKey, model), nil
	case "ollama":
		return NewOllamaGenerator(apiKey, model), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider %q — use anthropic, openai, gemini, or ollama", provider)
	}
}

// systemPrompt is shared across all providers.
const systemPrompt = `You are a company knowledge assistant. Answer the user's question using only the provided context.
For each fact you use, cite the source as [path:line_start-line_end].
If the context does not contain enough information to answer, say so explicitly.
Do not speculate beyond what the context shows.`

// buildContext assembles retrieved results into a prompt context string,
// stopping when maxRunes would be exceeded.
func buildContext(results []retrieval.Result, maxRunes int) string {
	var sb strings.Builder
	total := 0
	for _, r := range results {
		var entry string
		if r.DocChunk != nil {
			entry = fmt.Sprintf("[Source: %s (chunk %d)]\n%s\n\n",
				r.DocChunk.SourcePath, r.DocChunk.ChunkIndex, r.DocChunk.Content)
		} else if r.CodeNode != nil {
			entry = fmt.Sprintf("[Source: %s:%d-%d]\n%s\n\n",
				r.CodeNode.RepoPath, r.CodeNode.LineStart, r.CodeNode.LineEnd, r.CodeNode.Content)
		}
		if total+len([]rune(entry)) > maxRunes {
			break
		}
		sb.WriteString(entry)
		total += len([]rune(entry))
	}
	return sb.String()
}
