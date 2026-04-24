package generate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abagile/tokyo3-rag/internal/retrieval"
)

const (
	openaiEndpoint = "https://api.openai.com/v1/chat/completions"
	geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	ollamaBaseURL  = "http://localhost:11434"
)

// openaiCompatGenerator implements the OpenAI Chat Completions streaming API.
// It is also used for Gemini via Google's OpenAI-compatible endpoint.
type openaiCompatGenerator struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewOpenAI returns a Generator backed by OpenAI.
// Default model: gpt-4o.
func NewOpenAI(apiKey, model string) Generator {
	if model == "" {
		model = "gpt-4o"
	}
	return &openaiCompatGenerator{
		apiKey:  apiKey,
		model:   model,
		baseURL: openaiEndpoint,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// NewOllamaGenerator returns a Generator backed by a local Ollama server.
// baseURL defaults to http://localhost:11434 if empty; apiKey is unused.
// Default model: gemma3.
func NewOllamaGenerator(baseURL, model string) Generator {
	if baseURL == "" {
		baseURL = ollamaBaseURL
	}
	if model == "" {
		model = "gemma3"
	}
	return &openaiCompatGenerator{
		apiKey:  "",
		model:   model,
		baseURL: baseURL + "/v1/chat/completions",
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// NewGemini returns a Generator backed by Google Gemini via its OpenAI-compatible endpoint.
// Default model: gemini-2.0-flash.
func NewGemini(apiKey, model string) Generator {
	if model == "" {
		model = "gemini-2.0-flash"
	}
	return &openaiCompatGenerator{
		apiKey:  apiKey,
		model:   model,
		baseURL: geminiEndpoint,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (g *openaiCompatGenerator) Stream(ctx context.Context, query string, results []retrieval.Result, maxContextRunes int) <-chan Token {
	ch := make(chan Token, 32)
	go func() {
		defer close(ch)
		if err := g.stream(ctx, query, results, maxContextRunes, ch); err != nil {
			ch <- Token{Error: err}
		}
	}()
	return ch
}

func (g *openaiCompatGenerator) stream(ctx context.Context, query string, results []retrieval.Result, maxContextRunes int, ch chan<- Token) error {
	ctxStr := buildContext(results, maxContextRunes)
	userMsg := fmt.Sprintf("Context:\n%s\n\nQuestion: %s", ctxStr, query)

	body, err := json.Marshal(map[string]any{
		"model": g.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
		"max_tokens": 4096,
		"stream":     true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s http %d: %s", g.baseURL, resp.StatusCode, b)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- Token{Done: true}
			return nil
		}

		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if len(event.Choices) > 0 {
			if text := event.Choices[0].Delta.Content; text != "" {
				ch <- Token{Text: text}
			}
			if event.Choices[0].FinishReason != nil {
				ch <- Token{Done: true}
				return nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	ch <- Token{Done: true}
	return nil
}
