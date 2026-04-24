package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const ollamaBatch = 32

type ollamaProvider struct {
	docModel  string
	codeModel string
	baseURL   string
	client    *http.Client
}

// NewOllama returns an Ollama embedding provider.
//
// docModel is used for InputTypeDocument and InputTypeQuery (e.g. "nomic-embed-text").
// codeModel is used for InputTypeCode and InputTypeCodeQuery (e.g. "bge-m3").
// baseURL defaults to http://localhost:11434 if empty.
func NewOllama(docModel, codeModel, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &ollamaProvider{
		docModel:  docModel,
		codeModel: codeModel,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *ollamaProvider) EmbedTexts(ctx context.Context, texts []string, t InputType) ([][]float32, error) {
	model := o.docModel
	if t == InputTypeCode || t == InputTypeCodeQuery {
		model = o.codeModel
	}

	result := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += ollamaBatch {
		end := min(i+ollamaBatch, len(texts))
		vecs, err := o.embedBatch(ctx, texts[i:end], model)
		if err != nil {
			return nil, fmt.Errorf("batch %d-%d: %w", i, end, err)
		}
		copy(result[i:], vecs)
	}
	return result, nil
}

func (o *ollamaProvider) embedBatch(ctx context.Context, texts []string, model string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama http %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(result.Embeddings), len(texts))
	}
	return result.Embeddings, nil
}
