package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// embeddingsJSON is a batchEmbedContents response with n vectors of size dims; vector i starts
// with first+i.
func embeddingsJSON(t *testing.T, n, dims int, first float32) []byte {
	t.Helper()
	type values struct {
		Values []float32 `json:"values"`
	}
	resp := struct {
		Embeddings []values `json:"embeddings"`
	}{}
	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		v[0] = first + float32(i)
		resp.Embeddings = append(resp.Embeddings, values{Values: v})
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

// fakeGemini answers chat and embedding calls and records every request path and body.
type fakeGemini struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	dims   int // size of the vectors it returns
}

func (f *fakeGemini) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "batchEmbedContents") {
			var req struct {
				Requests []json.RawMessage `json:"requests"`
			}
			assert.NoError(t, json.Unmarshal(body, &req))
			_, _ = w.Write(embeddingsJSON(t, len(req.Requests), f.dims, 1))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"}}]}`))
	}
}

func (f *fakeGemini) start(t *testing.T) func(*genai.ClientConfig) {
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	}
}

// V4-03: the embedding model is configured separately from the chat model. Whatever the chat
// model is, an embedding is asked of the embedding model, at the stored size.
func TestEmbedder_UsesTheEmbeddingModelWhateverTheChatModel(t *testing.T) {
	for _, chatModel := range []string{config.DefaultGeminiModel, "some-other-chat-model"} {
		t.Run(chatModel, func(t *testing.T) {
			fake := &fakeGemini{dims: models.EmbeddingDimensions}
			withFake := fake.start(t)
			ctx := context.Background()
			chat, err := NewClient(ctx, "k", chatModel, withFake)
			require.NoError(t, err)
			embedder, err := NewEmbedder(ctx, "k", config.DefaultEmbeddingModel, models.EmbeddingDimensions, withFake)
			require.NoError(t, err)
			chat.UseEmbedder(embedder)

			_, err = chat.Chat(ctx, "sys", nil, "hello", nil, "")
			require.NoError(t, err)
			vec, err := chat.GenerateEmbedding(ctx, "a fact")
			require.NoError(t, err)
			assert.Len(t, vec, models.EmbeddingDimensions)

			require.Len(t, fake.paths, 2)
			assert.Contains(t, fake.paths[0], chatModel+":generateContent", "the chat goes to the chat model")
			assert.Contains(t, fake.paths[1], config.DefaultEmbeddingModel+":batchEmbedContents", "the embedding goes to the embedding model")
			assert.NotContains(t, fake.paths[1], chatModel)
			assert.Contains(t, fake.bodies[1], `"outputDimensionality":3072`, "the stored size is asked for explicitly")
		})
	}
}

// A vector of another size than the schema stores is refused, not returned.
func TestEmbedder_RefusesAVectorOfAnotherSize(t *testing.T) {
	fake := &fakeGemini{dims: 768}
	withFake := fake.start(t)
	embedder, err := NewEmbedder(context.Background(), "k", "another-embedding-model", models.EmbeddingDimensions, withFake)
	require.NoError(t, err)
	_, err = embedder.Embed(context.Background(), "a fact")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "768")
	assert.Contains(t, err.Error(), "schema migration")
}

// A batch returns one vector per text, in order, in one request.
func TestEmbedder_BatchIsOneRequestInOrder(t *testing.T) {
	fake := &fakeGemini{dims: 4}
	withFake := fake.start(t)
	embedder, err := NewEmbedder(context.Background(), "k", "m", 4, withFake)
	require.NoError(t, err)
	out, err := embedder.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, out, 3)
	for i, v := range out {
		assert.Equal(t, float32(1+i), v[0], "vector %d is out of order", i)
	}
	assert.Len(t, fake.paths, 1)

	_, err = embedder.EmbedBatch(context.Background(), make([]string, EmbedBatchSize+1))
	assert.Error(t, err, "more than one request's worth is refused")
}

// A response with fewer vectors than texts is an error, not a short result.
func TestEmbedder_ShortResponseIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(embeddingsJSON(t, 1, 4, 0))
	}))
	t.Cleanup(ts.Close)
	embedder, err := NewEmbedder(context.Background(), "k", "m", 4, func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	})
	require.NoError(t, err)
	_, err = embedder.EmbedBatch(context.Background(), []string{"a", "b"})
	assert.Error(t, err)
}

func TestEmbedder_NeedsKeyModelAndSize(t *testing.T) {
	ctx := context.Background()
	_, err := NewEmbedder(ctx, "", "m", 3)
	assert.ErrorContains(t, err, "GEMINI_API_KEY")
	_, err = NewEmbedder(ctx, "k", "", 3)
	assert.ErrorContains(t, err, "EMBEDDING_MODEL")
	_, err = NewEmbedder(ctx, "k", "m", 0)
	assert.Error(t, err)
}

// A chat client without an embedder says so instead of embedding with some model.
func TestClient_GenerateEmbedding_WithoutEmbedder(t *testing.T) {
	client, err := NewClient(context.Background(), "k", "")
	require.NoError(t, err)
	_, err = client.GenerateEmbedding(context.Background(), "x")
	assert.ErrorContains(t, err, "no embedding model")
}
