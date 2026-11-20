package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/genai"
)

// EmbedBatchSize is the most texts one embedding request carries. The Gemini API accepts up to
// 100 per batchEmbedContents call; half that keeps a request small and a failure cheap to retry.
const EmbedBatchSize = 50

// Embedder turns text into the vectors that memory recall searches by (V4-03). It is configured
// on its own (EMBEDDING_MODEL) and never by the chat model, so changing the chat model or
// provider cannot change which vector space the stored memories are in. Until a second
// embedding provider is supported it is always Gemini, with GEMINI_API_KEY.
type Embedder struct {
	client     *genai.Client
	model      string
	dimensions int
}

// NewEmbedder builds an embedder for model, whose vectors must have exactly dimensions numbers
// (the size the schema stores). opts adjust the client config, as for NewClient.
func NewEmbedder(ctx context.Context, apiKey, model string, dimensions int, opts ...func(*genai.ClientConfig)) (*Embedder, error) {
	if apiKey == "" {
		return nil, errors.New("gemini api key is required for embeddings (GEMINI_API_KEY)")
	}
	if model == "" {
		return nil, errors.New("an embedding model is required (EMBEDDING_MODEL)")
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("embedding size must be positive, got %d", dimensions)
	}
	cfg := &genai.ClientConfig{APIKey: apiKey}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 90 * time.Second}
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini embedding client: %w", err)
	}
	return &Embedder{client: client, model: model, dimensions: dimensions}, nil
}

// Model is the embedding model's name.
func (e *Embedder) Model() string { return e.model }

// Dimensions is the size of every vector this embedder returns.
func (e *Embedder) Dimensions() int { return e.dimensions }

// Embed returns the embedding of one text.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// EmbedBatch returns the embeddings of texts, in order, in one request. A vector of another size
// than Dimensions is refused rather than returned: stored next to the others it could not be
// compared with them, and the database would refuse it anyway.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > EmbedBatchSize {
		return nil, fmt.Errorf("at most %d texts per embedding request, got %d", EmbedBatchSize, len(texts))
	}
	contents := make([]*genai.Content, len(texts))
	for i, t := range texts {
		contents[i] = &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(t)}}
	}
	// The size is asked for explicitly. For the model every install has used so far it is the
	// model's own size, so nothing changes; a model whose native size is larger is cut to the
	// stored size (cosine distance does not need the vector renormalised).
	size := int32(e.dimensions)
	resp, err := e.client.Models.EmbedContent(ctx, e.model, contents, &genai.EmbedContentConfig{OutputDimensionality: &size})
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("asked for %d embeddings, got %d", len(texts), len(resp.Embeddings))
	}
	out := make([][]float32, len(texts))
	for i, emb := range resp.Embeddings {
		if emb == nil || len(emb.Values) != e.dimensions {
			got := 0
			if emb != nil {
				got = len(emb.Values)
			}
			return nil, fmt.Errorf("%s returned a vector of %d numbers; the schema stores %d (a different size needs a schema migration, docs/EMBEDDINGS.md)", e.model, got, e.dimensions)
		}
		out[i] = emb.Values
	}
	return out, nil
}
