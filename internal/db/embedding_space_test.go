package db

import (
	"context"
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migratedClient is a client on a fresh database built by the versioned migrations, as every
// install's is (AutoMigrate does not seed the embedding space).
func migratedClient(t *testing.T) *Client {
	t.Helper()
	c, err := Open(newEmptyTestDatabase(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	sqlDB, err := c.DB.DB()
	require.NoError(t, err)
	require.NoError(t, Migrate(context.Background(), sqlDB))
	return c
}

// fakeVector is a deterministic stand-in for an embedding model: the same text always gives the
// same vector, and model is part of it, so vectors of two "models" differ.
func fakeVector(model, text string, dims int) []float32 {
	v := make([]float32, dims)
	h := fnv.New64a()
	_, _ = h.Write([]byte(model + "\x00" + text))
	seed := h.Sum64()
	for i := range v {
		seed = seed*6364136223846793005 + 1442695040888963407
		v[i] = float32(int64(seed>>33)%2000-1000) / 1000
	}
	return v
}

func fakeEmbed(model string, dims int, calls *int) EmbedBatchFunc {
	return func(_ context.Context, texts []string) ([][]float32, error) {
		if calls != nil {
			*calls++
		}
		out := make([][]float32, len(texts))
		for i, t := range texts {
			out[i] = fakeVector(model, t, dims)
		}
		return out, nil
	}
}

// addMemory stores a memory with the given vector (nil: no embedding, a zero vector).
func addMemory(t *testing.T, c *Client, household, fact string, vec []float32) uint {
	t.Helper()
	if vec == nil {
		vec = make([]float32, models.EmbeddingDimensions)
	}
	var id uint
	require.NoError(t, c.DB.Raw(`INSERT INTO topic_memories
		(user_id, household_id, family_group, topic, subtopic, tags_json, fact, scope, created_by_user_id, valid_from, is_pinned, embedding, created_at)
		VALUES ('u1', ?, 'fam', 'personal', '', '[]', ?, 'personal', 'u1', now(), false, ?, now()) RETURNING id`,
		household, fact, pgvector.NewVector(vec)).Scan(&id).Error)
	return id
}

func testHousehold(t *testing.T, c *Client) string {
	t.Helper()
	var id string
	require.NoError(t, c.DB.Raw(`INSERT INTO households (name, legacy_label, created_at, updated_at)
		VALUES ('Fam', 'fam', now(), now()) RETURNING id`).Scan(&id).Error)
	return id
}

// vectorsByID is every stored vector, as Postgres prints it, keyed by memory ID.
func vectorsByID(t *testing.T, c *Client) map[uint]string {
	t.Helper()
	var rows []struct {
		ID  uint
		Vec string
	}
	require.NoError(t, c.DB.Raw(`SELECT id, embedding::text AS vec FROM topic_memories ORDER BY id`).Scan(&rows).Error)
	out := map[uint]string{}
	for _, r := range rows {
		out[r.ID] = r.Vec
	}
	return out
}

func storedVector(t *testing.T, c *Client, id uint) []float32 {
	t.Helper()
	var v pgvector.Vector
	require.NoError(t, c.DB.Raw(`SELECT embedding FROM topic_memories WHERE id = ?`, id).Row().Scan(&v))
	return v.Slice()
}

func recorded(t *testing.T, c *Client) models.EmbeddingSpace {
	t.Helper()
	st, err := c.EmbeddingStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, st.Recorded)
	return *st.Recorded
}

// Every install so far embedded with gemini-embedding-001 at 3,072 dimensions; the migration
// records exactly that, and the schema stores that size.
func TestEmbeddingSpace_MigrationRecordsTheModelEveryInstallUsed(t *testing.T) {
	c := migratedClient(t)
	st, err := c.EmbeddingStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, st.Recorded)
	assert.Equal(t, "gemini-embedding-001", st.Recorded.Model)
	assert.Equal(t, models.EmbeddingDimensions, st.Recorded.Dimensions)
	assert.Equal(t, models.EmbeddingDimensions, st.ColumnDimensions)

	// The table holds one row and no more.
	err = c.DB.Exec(`INSERT INTO embedding_space (id, model, dimensions, updated_at) VALUES (2, 'x', 1, now())`).Error
	assert.Error(t, err, "a second embedding space must be refused")
}

func TestCheckEmbeddingSpace(t *testing.T) {
	ctx := context.Background()
	const stored = "gemini-embedding-001"

	t.Run("the recorded model passes", func(t *testing.T) {
		c := migratedClient(t)
		addMemory(t, c, testHousehold(t, c), "likes tea", fakeVector(stored, "likes tea", models.EmbeddingDimensions))
		assert.NoError(t, c.CheckEmbeddingSpace(ctx, stored))
	})

	t.Run("another model is refused and nothing changes", func(t *testing.T) {
		c := migratedClient(t)
		addMemory(t, c, testHousehold(t, c), "likes tea", fakeVector(stored, "likes tea", models.EmbeddingDimensions))
		before := vectorsByID(t, c)
		err := c.CheckEmbeddingSpace(ctx, "another-embedding-model")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrEmbeddingSpaceMismatch))
		assert.Contains(t, err.Error(), "mypa embeddings reembed", "the error must say what to run")
		assert.Contains(t, err.Error(), stored, "the error must name the model the vectors came from")
		assert.Equal(t, before, vectorsByID(t, c))
		assert.Equal(t, stored, recorded(t, c).Model)
	})

	t.Run("an install with nothing embedded takes the configured model", func(t *testing.T) {
		c := migratedClient(t)
		addMemory(t, c, testHousehold(t, c), "saved without an embedding", nil)
		require.NoError(t, c.CheckEmbeddingSpace(ctx, "another-embedding-model"))
		assert.Equal(t, "another-embedding-model", recorded(t, c).Model)
	})
}

// The full re-embed replaces every vector and the recorded model together.
func TestReembed_ReplacesEveryVectorAndTheModel(t *testing.T) {
	ctx := context.Background()
	c := migratedClient(t)
	hh := testHousehold(t, c)
	const oldModel, newModel = "gemini-embedding-001", "new-embedding-model"
	dims := models.EmbeddingDimensions
	facts := []string{"allergic to peanuts", "the car is due for service in March", "likes jasmine tea", "plays the violin"}
	ids := make([]uint, len(facts))
	for i, f := range facts {
		ids[i] = addMemory(t, c, hh, f, fakeVector(oldModel, f, dims))
	}
	missing := addMemory(t, c, hh, "saved without an embedding", nil)
	empty := addMemory(t, c, hh, "", fakeVector(oldModel, "stale", dims))

	calls := 0
	var progress []int
	res, err := c.Reembed(ctx, fakeEmbed(newModel, dims, &calls), ReembedOptions{
		Model: newModel, BatchSize: 2, Progress: func(done, _ int) { progress = append(progress, done) },
	})
	require.NoError(t, err)
	assert.Equal(t, ReembedResult{Previous: oldModel, Model: newModel, Dimensions: dims, Embedded: 5, Empty: 1}, res)
	assert.Equal(t, 3, calls, "five texts in batches of two")
	assert.Equal(t, []int{2, 4, 6}, progress)

	for i, id := range ids {
		assert.Equal(t, fakeVector(newModel, facts[i], dims), storedVector(t, c, id), "memory %q", facts[i])
	}
	assert.Equal(t, fakeVector(newModel, "saved without an embedding", dims), storedVector(t, c, missing))
	assert.Equal(t, make([]float32, dims), storedVector(t, c, empty), "an empty memory keeps no vector from the old model")
	assert.Equal(t, newModel, recorded(t, c).Model)
	assert.NoError(t, c.CheckEmbeddingSpace(ctx, newModel))
	assert.ErrorIs(t, c.CheckEmbeddingSpace(ctx, oldModel), ErrEmbeddingSpaceMismatch)

	// Recall works in the new space, through the index expression the app searches with.
	var nearest string
	require.NoError(t, c.DB.Raw(`SELECT fact FROM topic_memories ORDER BY `+halfvecCosineDistanceSQL+` LIMIT 1`,
		pgvector.NewVector(fakeVector(newModel, "likes jasmine tea", dims))).Scan(&nearest).Error)
	assert.Equal(t, "likes jasmine tea", nearest)
}

// If any batch fails, nothing is changed: the vectors stay in one space.
func TestReembed_AFailureChangesNothing(t *testing.T) {
	ctx := context.Background()
	c := migratedClient(t)
	hh := testHousehold(t, c)
	for _, f := range []string{"one", "two", "three"} {
		addMemory(t, c, hh, f, fakeVector("gemini-embedding-001", f, models.EmbeddingDimensions))
	}
	before := vectorsByID(t, c)

	calls := 0
	failing := func(ctx context.Context, texts []string) ([][]float32, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("quota exceeded")
		}
		return fakeEmbed("new", models.EmbeddingDimensions, nil)(ctx, texts)
	}
	_, err := c.Reembed(ctx, failing, ReembedOptions{Model: "new", BatchSize: 1})
	require.ErrorContains(t, err, "quota exceeded")
	assert.Equal(t, before, vectorsByID(t, c), "a failed re-embed must leave every vector as it was")
	assert.Equal(t, "gemini-embedding-001", recorded(t, c).Model)
}

// A model whose vectors have another size than the schema stores is refused, and nothing changes.
func TestReembed_AnotherSizeNeedsTheMigrationFirst(t *testing.T) {
	ctx := context.Background()
	c := migratedClient(t)
	addMemory(t, c, testHousehold(t, c), "one", fakeVector("gemini-embedding-001", "one", models.EmbeddingDimensions))
	before := vectorsByID(t, c)
	_, err := c.Reembed(ctx, fakeEmbed("small", 768, nil), ReembedOptions{Model: "small"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema migration")
	assert.Equal(t, before, vectorsByID(t, c))
	assert.Equal(t, "gemini-embedding-001", recorded(t, c).Model)
}

// --missing fills only the memories without an embedding (BL-24), in the recorded space.
func TestReembed_MissingOnly(t *testing.T) {
	ctx := context.Background()
	c := migratedClient(t)
	hh := testHousehold(t, c)
	const model = "gemini-embedding-001"
	dims := models.EmbeddingDimensions
	kept := addMemory(t, c, hh, "embedded", fakeVector(model, "embedded", dims))
	keptVec := storedVector(t, c, kept)
	gap := addMemory(t, c, hh, "saved before V4-06", nil)

	st, err := c.EmbeddingStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), st.Memories)
	assert.Equal(t, int64(1), st.WithoutEmbedding)

	_, err = c.Reembed(ctx, fakeEmbed("other", dims, nil), ReembedOptions{Model: "other", MissingOnly: true})
	require.Error(t, err, "--missing must not mix a second model into the recorded space")
	assert.Equal(t, make([]float32, dims), storedVector(t, c, gap))

	res, err := c.Reembed(ctx, fakeEmbed(model, dims, nil), ReembedOptions{Model: model, MissingOnly: true})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Embedded)
	assert.Equal(t, keptVec, storedVector(t, c, kept), "an embedded memory is left alone")
	assert.Equal(t, fakeVector(model, "saved before V4-06", dims), storedVector(t, c, gap))
	st, err = c.EmbeddingStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), st.WithoutEmbedding)
}

// sizeChangeMigration is the SQL docs/EMBEDDINGS.md gives for moving to a model of another size,
// read from the doc itself so the procedure there is the one tested here.
func sizeChangeMigration(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "EMBEDDINGS.md"))
	require.NoError(t, err)
	up, err := sizeChangeMigrationIn(doc)
	require.NoError(t, err)
	return up
}

// sizeChangeMigrationIn finds the size-change migration's Up SQL in the doc's text.
func sizeChangeMigrationIn(doc []byte) (string, error) {
	m := regexp.MustCompile("(?s)<!-- size-change-migration -->\\s*```sql\\r?\\n(.*?)```").FindSubmatch(doc) // BL-59: LF or CRLF
	if m == nil {
		return "", errors.New("docs/EMBEDDINGS.md must hold the size-change migration in a marked sql block")
	}
	sql := strings.ReplaceAll(string(m[1]), "\r\n", "\n")
	up := strings.SplitN(sql, "-- +goose Down", 2)[0]
	if !strings.Contains(up, "-- +goose Up") {
		return "", errors.New("the size-change migration has no -- +goose Up")
	}
	return strings.Replace(up, "-- +goose Up", "", 1), nil
}

// BL-59: the doc is read with Windows line endings too (a Windows checkout, or the race run in a
// container that mounts one), and gives the same SQL.
func TestSizeChangeMigration_IsFoundWithEitherLineEnding(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "EMBEDDINGS.md"))
	require.NoError(t, err)
	lf := []byte(strings.ReplaceAll(string(doc), "\r\n", "\n"))
	crlf := []byte(strings.ReplaceAll(string(lf), "\n", "\r\n"))

	fromLF, err := sizeChangeMigrationIn(lf)
	require.NoError(t, err)
	fromCRLF, err := sizeChangeMigrationIn(crlf)
	require.NoError(t, err, "a CRLF copy of the doc")
	assert.Equal(t, fromLF, fromCRLF)
	assert.Contains(t, fromLF, "ALTER TABLE")
}

// The documented path to an embedding model of another size: the schema migration from
// docs/EMBEDDINGS.md, then a full re-embed. After it the stored vectors, the index and the
// recorded space all have the new size, and recall finds memories by meaning again.
func TestReembed_ToAnotherSizeAfterTheDocumentedMigration(t *testing.T) {
	ctx := context.Background()
	c := migratedClient(t)
	hh := testHousehold(t, c)
	facts := []string{"allergic to peanuts", "likes jasmine tea", "plays the violin"}
	for _, f := range facts {
		addMemory(t, c, hh, f, fakeVector("gemini-embedding-001", f, models.EmbeddingDimensions))
	}

	require.NoError(t, c.DB.Exec(sizeChangeMigration(t)).Error)
	st, err := c.EmbeddingStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 768, st.ColumnDimensions, "the documented migration changes the stored size (its example is 768)")
	assert.Equal(t, st.Memories, st.WithoutEmbedding, "the migration leaves no vector from the old model")

	res, err := c.Reembed(ctx, fakeEmbed("small-model", 768, nil), ReembedOptions{Model: "small-model"})
	require.NoError(t, err)
	assert.Equal(t, 3, res.Embedded)
	assert.Equal(t, 768, res.Dimensions)
	rec := recorded(t, c)
	assert.Equal(t, "small-model", rec.Model)
	assert.Equal(t, 768, rec.Dimensions)
	assert.NoError(t, c.CheckEmbeddingSpace(ctx, "small-model"))

	var indexDef string
	require.NoError(t, c.DB.Raw(`SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_topic_memories_embedding'`).Scan(&indexDef).Error)
	assert.Contains(t, indexDef, "halfvec(768)", "the HNSW index is rebuilt for the new size")
	var nearest string
	require.NoError(t, c.DB.Raw(`SELECT fact FROM topic_memories ORDER BY embedding::halfvec(768) <=> ?::halfvec(768) LIMIT 1`,
		pgvector.NewVector(fakeVector("small-model", "plays the violin", 768))).Scan(&nearest).Error)
	assert.Equal(t, "plays the violin", nearest)
}
