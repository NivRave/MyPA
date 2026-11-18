package db

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func unitVec(dim int, noiseDim int, noise float32) pgvector.Vector {
	v := make([]float32, 3072)
	v[dim] = 1
	if noiseDim >= 0 {
		v[noiseDim] = noise
	}
	return pgvector.NewVector(v)
}

// explainUsesIndex runs EXPLAIN with seq scans disabled (tiny tables would otherwise never use an
// index) and reports whether the plan scans the named index.
func explainUsesIndex(t *testing.T, gdb *gorm.DB, orderExpr, index string) (bool, string) {
	t.Helper()
	var plan []string
	err := gdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL enable_seqscan = off").Error; err != nil {
			return err
		}
		rows, err := tx.Raw("EXPLAIN SELECT id FROM topic_memories ORDER BY "+orderExpr+" LIMIT 5", unitVec(0, -1, 0)).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				return err
			}
			plan = append(plan, line)
		}
		return rows.Err()
	})
	require.NoError(t, err)
	joined := strings.Join(plan, "\n")
	return strings.Contains(joined, index), joined
}

// P2-3 acceptance: the similarity ORDER BY must match the HNSW expression index.
func TestVectorSearch_OrderExpressionUsesHNSWIndex(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	used, plan := explainUsesIndex(t, client.DB, halfvecCosineDistanceSQL, "idx_topic_memories_embedding")
	assert.True(t, used, "query must use the HNSW index; plan:\n%s", plan)

	// Documents the original bug: plain vector distance cannot use a halfvec expression index.
	usedOld, _ := explainUsesIndex(t, client.DB, "embedding <=> ?", "idx_topic_memories_embedding")
	assert.False(t, usedOld, "plain `embedding <=> ?` is not index-eligible (the P2-3 bug)")
}

// With the index in use, HNSW returns the ef_search (40) nearest rows table-wide and the tenant
// filter runs afterwards. When other tenants own the nearest rows, a plain index scan returns
// NOTHING for this user. Iterative scans (pgvector >= 0.8) keep scanning until LIMIT is met.
func TestVectorSearch_TenantFilteredRecallWithIndex(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// One session for every query, with the planner pushed onto the HNSW path: on a small table it
	// would otherwise pick a B-tree index + sort (exact, but not what large production tables do).
	sqlDB, err := client.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	require.NoError(t, client.DB.Exec("SET enable_seqscan = off").Error)
	require.NoError(t, client.DB.Exec("SET enable_sort = off").Error)

	repo := NewMemoryV2Repo(client.DB)
	query := mixVec(1.0, 0)

	// 120 memories close to the query, owned by ANOTHER family.
	for i := 0; i < 120; i++ {
		require.NoError(t, repo.SaveTopicMemory(ctx, &models.TopicMemory{
			UserID: fmt.Sprintf("other-%d", i), FamilyGroup: "fam_other", Scope: "family",
			Topic: "general", Subtopic: "fact", Fact: fmt.Sprintf("other fact %d", i),
			Embedding: mixVec(0.9, int64(1000+i)),
		}))
	}
	// 3 personal memories for our user, farther away.
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.SaveTopicMemory(ctx, &models.TopicMemory{
			UserID: "user_a", FamilyGroup: "fam_a", Scope: "personal",
			Topic: "general", Subtopic: "fact", Fact: fmt.Sprintf("user a fact %d", i),
			Embedding: mixVec(0.2, int64(5000+i)),
		}))
	}

	// Precondition: a plain filtered HNSW scan really does lose this user's rows here.
	var naive int64
	require.NoError(t, client.DB.Raw(
		"SELECT count(*) FROM (SELECT id FROM topic_memories WHERE user_id = 'user_a' ORDER BY "+halfvecCosineDistanceSQL+" LIMIT 5) s",
		query).Scan(&naive).Error)
	require.Less(t, naive, int64(3), "test setup must reproduce the filtered-HNSW recall loss")

	got, err := repo.SearchSimilarMemories(ctx, "user_a", "fam_a", query, 5)
	require.NoError(t, err)
	assert.Len(t, got, 3, "all of the user's memories must be found even when other tenants own the nearest neighbours")
	for _, m := range got {
		assert.Equal(t, "user_a", m.UserID, "tenant isolation must hold")
	}
}

func TestPgvectorVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.8.0", true}, {"0.8.1", true}, {"0.10.0", true}, {"1.0", true},
		{"0.7.4", false}, {"0.5.1", false}, {"", false}, {"garbage", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, versionAtLeast(c.v, 0, 8), c.v)
	}
}

// mixVec returns a normalized dense vector: weight*query + (1-weight)*noise(seed), where query is a
// fixed random direction. Dense, correlated vectors avoid the degenerate unreachable-node graphs
// that exactly orthogonal test vectors produce in HNSW.
func mixVec(weight float64, seed int64) pgvector.Vector {
	q := rand.New(rand.NewSource(42))
	n := rand.New(rand.NewSource(seed + 7))
	v := make([]float32, 3072)
	var norm float64
	for i := range v {
		x := weight*q.NormFloat64() + (1-weight)*n.NormFloat64()
		v[i] = float32(x)
		norm += x * x
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return pgvector.NewVector(v)
}

// Similarity search must return nearest-first. GORM's Order() silently drops clause.Expr values,
// so `Order(gorm.Expr("embedding <=> ?", v))` produced NO ORDER BY at all: results were the first
// N matching rows in physical order. The nearest row is inserted LAST here so insertion order
// cannot satisfy the assertions by accident.
func TestVectorSearch_ReturnsNearestFirst(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := NewMemoryV2Repo(client.DB)

	weights := []float64{0.1, 0.3, 0.5, 0.7, 0.95} // increasing similarity to the query
	for i, w := range weights {
		require.NoError(t, repo.SaveTopicMemory(ctx, &models.TopicMemory{
			UserID: "user_o", FamilyGroup: "fam_o", Scope: "personal", Topic: "general", Subtopic: "fact",
			Fact: fmt.Sprintf("w=%.2f", w), Embedding: mixVec(w, int64(100+i)),
		}))
	}

	got, err := repo.SearchSimilarMemories(ctx, "user_o", "fam_o", mixVec(1.0, 0), 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"w=0.95", "w=0.70", "w=0.50"}, []string{got[0].Fact, got[1].Fact, got[2].Fact})

	// The engine's retrieval entry point (Client.SearchSimilarMemories) orders too.
	top, err := client.SearchSimilarMemories(ctx, "user_o", "fam_o", mixVec(1.0, 0), 1)
	require.NoError(t, err)
	require.Len(t, top, 1)
	assert.Equal(t, "w=0.95", top[0].Fact)
}
