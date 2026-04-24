package embed

import "context"

// InputType tells the embedding model how the text will be used.
type InputType int

const (
	InputTypeDocument  InputType = iota // text to be indexed
	InputTypeQuery                      // text used as a search query
	InputTypeCode                       // source code to be indexed
	InputTypeCodeQuery                  // query over a code index
)

// Provider generates vector embeddings for text.
type Provider interface {
	// EmbedTexts returns one embedding per input string.
	// Implementations must handle batching internally.
	EmbedTexts(ctx context.Context, texts []string, t InputType) ([][]float32, error)
}
