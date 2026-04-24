package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	voyageEndpoint = "https://api.voyageai.com/v1/embeddings"
	voyageBatch    = 96 // stay under Voyage's 128 limit
	maxRetries     = 3
)

type voyageProvider struct {
	apiKey string
	client *http.Client
}

// NewVoyage returns a Voyage AI embedding provider.
// Use VOYAGE_API_KEY for the key.
func NewVoyage(apiKey string) Provider {
	return &voyageProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (v *voyageProvider) EmbedTexts(ctx context.Context, texts []string, t InputType) ([][]float32, error) {
	model, inputType := voyageModel(t)
	result := make([][]float32, len(texts))

	for i := 0; i < len(texts); i += voyageBatch {
		end := min(i+voyageBatch, len(texts))
		batch := texts[i:end]
		vecs, err := v.embedBatch(ctx, batch, model, inputType)
		if err != nil {
			return nil, fmt.Errorf("batch %d-%d: %w", i, end, err)
		}
		copy(result[i:], vecs)
	}
	return result, nil
}

func (v *voyageProvider) embedBatch(ctx context.Context, texts []string, model, inputType string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{
		"input":      texts,
		"model":      model,
		"input_type": inputType,
	})
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Detail string `json:"detail"` // error message from Voyage
	}

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(math.Pow(2, float64(attempt))) * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, voyageEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
		req.Header.Set("Content-Type", "application/json")

		httpResp, err := v.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer httpResp.Body.Close()

		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		if httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode >= 500 {
			lastErr = fmt.Errorf("voyage http %d", httpResp.StatusCode)
			continue
		}
		if httpResp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("voyage http %d: %s", httpResp.StatusCode, respBody)
		}

		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("decode voyage response: %w", err)
		}
		if resp.Detail != "" {
			return nil, fmt.Errorf("voyage error: %s", resp.Detail)
		}

		out := make([][]float32, len(texts))
		for _, d := range resp.Data {
			if d.Index < len(out) {
				out[d.Index] = d.Embedding
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("voyage: exhausted retries: %w", lastErr)
}

func voyageModel(t InputType) (model, inputType string) {
	switch t {
	case InputTypeCode, InputTypeCodeQuery:
		model = "voyage-code-3"
	default:
		model = "voyage-3-large"
	}
	switch t {
	case InputTypeQuery, InputTypeCodeQuery:
		inputType = "query"
	default:
		inputType = "document"
	}
	return
}
