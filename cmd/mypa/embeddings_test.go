package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// fakeGeminiAPI answers chat and embedding requests and records which model each went to.
type fakeGeminiAPI struct {
	mu    sync.Mutex
	paths []string
}

func (f *fakeGeminiAPI) option(t *testing.T) func(*genai.ClientConfig) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "batchEmbedContents") {
			var req struct {
				Requests []json.RawMessage `json:"requests"`
			}
			assert.NoError(t, json.Unmarshal(body, &req))
			type values struct {
				Values []float32 `json:"values"`
			}
			var resp struct {
				Embeddings []values `json:"embeddings"`
			}
			for range req.Requests {
				v := make([]float32, models.EmbeddingDimensions)
				v[0] = 1
				resp.Embeddings = append(resp.Embeddings, values{Values: v})
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"}}]}`))
	}))
	t.Cleanup(ts.Close)
	target, err := url.Parse(ts.URL)
	require.NoError(t, err)
	return func(cfg *genai.ClientConfig) {
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(r)
		})}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// embeddedInstall is a migrated database holding memories embedded by the model every install
// has used, as a live install's does.
func embeddedInstall(t *testing.T) *db.Client {
	t.Helper()
	client, err := db.Open(pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	sqlDB, err := client.DB.DB()
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background(), sqlDB))

	var household string
	require.NoError(t, client.DB.Raw(`INSERT INTO households (name, legacy_label, created_at, updated_at)
		VALUES ('Fam', 'fam', now(), now()) RETURNING id`).Scan(&household).Error)
	for i, fact := range []string{"allergic to peanuts", "likes jasmine tea", "plays the violin"} {
		v := make([]float32, models.EmbeddingDimensions)
		v[i], v[i+100] = 1, 0.25
		require.NoError(t, client.DB.Exec(`INSERT INTO topic_memories
			(user_id, household_id, family_group, topic, subtopic, tags_json, fact, scope, created_by_user_id, valid_from, is_pinned, embedding, created_at)
			VALUES ('u1', ?, 'fam', 'personal', '', '[]', ?, 'personal', 'u1', now(), false, ?, now())`,
			household, fact, pgvector.NewVector(v)).Error)
	}
	return client
}

// storedEmbeddingState is everything about the stored vectors that must not change: each vector,
// the column type, the vector indexes and the recorded embedding space.
func storedEmbeddingState(t *testing.T, client *db.Client) string {
	t.Helper()
	var parts []string
	var vectors []string
	require.NoError(t, client.DB.Raw(`SELECT id || ':' || embedding::text FROM topic_memories ORDER BY id`).Scan(&vectors).Error)
	parts = append(parts, vectors...)
	var column string
	require.NoError(t, client.DB.Raw(`SELECT format_type(atttypid, atttypmod) FROM pg_attribute
		WHERE attrelid = 'public.topic_memories'::regclass AND attname = 'embedding'`).Scan(&column).Error)
	parts = append(parts, "column "+column)
	var indexes []string
	require.NoError(t, client.DB.Raw(`SELECT indexdef FROM pg_indexes WHERE indexname LIKE '%_embedding' ORDER BY indexname`).Scan(&indexes).Error)
	parts = append(parts, indexes...)
	var space string
	require.NoError(t, client.DB.Raw(`SELECT model || '/' || dimensions || '/' || updated_at::text FROM embedding_space`).Scan(&space).Error)
	parts = append(parts, "space "+space)
	return strings.Join(parts, "\n")
}

// V4-03 "done when": changing the chat model leaves the halfvec(3072) data untouched. The app's
// start-up and a turn's model calls run once per chat model; afterwards every stored vector, the
// column, the indexes and the recorded model are exactly as before, and every embedding went to
// the embedding model whatever the chat model was. (The chat model is the provider's only setting
// until V4-02; its provider layer chats through the same client and keeps this embedder.)
func TestChangingTheChatModelLeavesTheStoredVectorsUntouched(t *testing.T) {
	ctx := context.Background()
	client := embeddedInstall(t)
	before := storedEmbeddingState(t, client)
	require.Contains(t, before, "column vector(3072)")
	require.Contains(t, before, "halfvec(3072)")
	require.Contains(t, before, "space gemini-embedding-001/3072/")

	for _, chatModel := range []string{config.DefaultGeminiModel, "another-chat-model", "yet-another-chat-model"} {
		t.Run(chatModel, func(t *testing.T) {
			t.Setenv("GEMINI_API_KEY", "test-key")
			t.Setenv("GEMINI_MODEL", chatModel)
			t.Setenv("EMBEDDING_MODEL", "")
			cfg, err := config.Load()
			require.NoError(t, err)
			require.Equal(t, chatModel, cfg.Gemini.Model)

			fake := &fakeGeminiAPI{}
			withFake := fake.option(t)
			// What the worker does at start: migrations, then the embeddings step.
			sqlDB, err := client.DB.DB()
			require.NoError(t, err)
			require.NoError(t, db.Migrate(ctx, sqlDB))
			embedder, err := prepareEmbeddings(ctx, cfg, client, withFake)
			require.NoError(t, err, "a chat model change must not stop the app")
			assert.Equal(t, config.DefaultEmbeddingModel, embedder.Model())

			// A turn: a chat call and the embedding of the message.
			chat, err := llm.NewClient(ctx, cfg.Gemini.APIKey, cfg.Gemini.Model, withFake)
			require.NoError(t, err)
			chat.UseEmbedder(embedder)
			_, err = chat.Chat(ctx, "sys", nil, "what do I like to drink?", nil, "")
			require.NoError(t, err)
			_, err = chat.GenerateEmbedding(ctx, "what do I like to drink?")
			require.NoError(t, err)
			require.Len(t, fake.paths, 2)
			assert.Contains(t, fake.paths[0], chatModel+":generateContent")
			assert.Contains(t, fake.paths[1], config.DefaultEmbeddingModel+":batchEmbedContents")

			assert.Equal(t, before, storedEmbeddingState(t, client), "the stored vectors must be untouched by a chat model change")
		})
	}
}

// Changing the embedding model without a re-embed stops the app with instructions, and still
// changes nothing.
func TestChangingTheEmbeddingModelWithoutReembedRefusesToStart(t *testing.T) {
	ctx := context.Background()
	client := embeddedInstall(t)
	before := storedEmbeddingState(t, client)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("EMBEDDING_MODEL", "another-embedding-model")
	cfg, err := config.Load()
	require.NoError(t, err)

	_, err = prepareEmbeddings(ctx, cfg, client)
	require.Error(t, err)
	assert.True(t, errors.Is(err, db.ErrEmbeddingSpaceMismatch))
	assert.Contains(t, err.Error(), "mypa embeddings reembed")
	assert.Equal(t, before, storedEmbeddingState(t, client))
}

func stoppedApp(context.Context, string) (int, int, error) { return 0, 0, nil }

func embeddingsTestDeps(client *db.Client, model string, out io.Writer, calls *int) embeddingsDeps {
	return embeddingsDeps{
		Store: client,
		Model: model,
		NewEmbedder: func(_ context.Context, dims int) (db.EmbedBatchFunc, error) {
			return func(_ context.Context, texts []string) ([][]float32, error) {
				*calls++
				out := make([][]float32, len(texts))
				for i := range texts {
					v := make([]float32, dims)
					v[dims-1-i%7] = 1
					out[i] = v
				}
				return out, nil
			}, nil
		},
		Probe: stoppedApp,
		Out:   out,
	}
}

// The command's whole path: status, a re-embed into another model, status again; and the app is
// started afterwards with the new model only.
func TestEmbeddingsCommand_ReembedIntoAnotherModel(t *testing.T) {
	ctx := context.Background()
	client := embeddedInstall(t)
	var out bytes.Buffer
	calls := 0
	deps := embeddingsTestDeps(client, "new-embedding-model", &out, &calls)

	require.Equal(t, 0, runEmbeddings(ctx, []string{"status"}, deps), out.String())
	assert.Contains(t, out.String(), "gemini-embedding-001 (3072 dimensions")
	assert.Contains(t, out.String(), "Memories:           3, of which 0 have no embedding")
	assert.Contains(t, out.String(), "run `mypa embeddings reembed` before starting the app")

	out.Reset()
	require.Equal(t, 0, runEmbeddings(ctx, []string{"reembed"}, deps), out.String())
	assert.Contains(t, out.String(), "✓ embedded 3 memories with new-embedding-model (3072 dimensions)")
	assert.Contains(t, out.String(), "changed from gemini-embedding-001 to new-embedding-model")
	assert.Equal(t, 1, calls)

	out.Reset()
	require.Equal(t, 0, runEmbeddings(ctx, []string{"status"}, deps))
	assert.Contains(t, out.String(), "✓ Every memory is embedded with the configured model.")

	assert.NoError(t, client.CheckEmbeddingSpace(ctx, "new-embedding-model"))
	assert.ErrorIs(t, client.CheckEmbeddingSpace(ctx, "gemini-embedding-001"), db.ErrEmbeddingSpaceMismatch)
}

// A re-embed refuses while the app consumes messages, and on a failed embedding reports that
// nothing changed.
func TestEmbeddingsCommand_RefusesWhileTheAppRunsAndReportsFailures(t *testing.T) {
	ctx := context.Background()
	client := embeddedInstall(t)
	before := storedEmbeddingState(t, client)
	var out bytes.Buffer
	calls := 0
	deps := embeddingsTestDeps(client, "new-embedding-model", &out, &calls)

	deps.Probe = func(context.Context, string) (int, int, error) { return 0, 1, nil }
	assert.Equal(t, 1, runEmbeddings(ctx, []string{"reembed"}, deps))
	assert.Contains(t, out.String(), "the app is running")
	assert.Equal(t, 0, calls, "nothing is embedded while the app runs")

	deps.Probe = func(context.Context, string) (int, int, error) { return 0, 0, errors.New("connection refused") }
	out.Reset()
	assert.Equal(t, 1, runEmbeddings(ctx, []string{"reembed"}, deps))
	assert.Contains(t, out.String(), "--skip-stop-check")

	deps.Probe = stoppedApp
	deps.NewEmbedder = func(context.Context, int) (db.EmbedBatchFunc, error) {
		return func(context.Context, []string) ([][]float32, error) { return nil, errors.New("quota exceeded") }, nil
	}
	out.Reset()
	assert.Equal(t, 1, runEmbeddings(ctx, []string{"reembed"}, deps))
	assert.Contains(t, out.String(), "quota exceeded")
	assert.Contains(t, out.String(), "Nothing was changed")
	assert.Equal(t, before, storedEmbeddingState(t, client))

	out.Reset()
	assert.Equal(t, 2, runEmbeddings(ctx, []string{"bogus"}, deps))
	assert.Contains(t, out.String(), "Usage: mypa embeddings")
}
