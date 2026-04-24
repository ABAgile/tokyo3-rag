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
	anthropicEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicVersion  = "2023-06-01"
)

type claudeGenerator struct {
	apiKey string
	model  string
	client *http.Client
}

// NewClaude returns a Generator backed by Anthropic Claude.
// Default model: claude-sonnet-4-6.
func NewClaude(apiKey, model string) Generator {
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &claudeGenerator{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (g *claudeGenerator) Stream(ctx context.Context, query string, results []retrieval.Result, maxContextRunes int) <-chan Token {
	ch := make(chan Token, 32)
	go func() {
		defer close(ch)
		if err := g.stream(ctx, query, results, maxContextRunes, ch); err != nil {
			ch <- Token{Error: err}
		}
	}()
	return ch
}

func (g *claudeGenerator) stream(ctx context.Context, query string, results []retrieval.Result, maxContextRunes int, ch chan<- Token) error {
	ctxStr := buildContext(results, maxContextRunes)
	userMsg := fmt.Sprintf("Context:\n%s\n\nQuestion: %s", ctxStr, query)

	body, err := json.Marshal(map[string]any{
		"model":      g.model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userMsg},
		},
		"stream": true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic http %d: %s", resp.StatusCode, b)
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
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			ch <- Token{Text: event.Delta.Text}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	ch <- Token{Done: true}
	return nil
}
