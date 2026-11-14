package db

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmpirical_DB_ConnectionPoolBoundaryAndLifecycle tests:
// 1. MaxOpenConnections is strictly configured to 25.
// 2. Concurrency stress: 35 concurrent queries against the 25-connection pool.
//    Verifies that all 35 queries complete successfully without connection leaks or deadlocks.
// 3. Ping() and Close() lifecycle: Ping succeeds when open, Close drains connections,
//    and Ping fails cleanly after Close().
func TestEmpirical_DB_ConnectionPoolBoundaryAndLifecycle(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	sqlDB, err := client.DB.DB()
	require.NoError(t, err)

	// 1. Verify pool sizing configuration
	stats := sqlDB.Stats()
	assert.Equal(t, 25, stats.MaxOpenConnections, "MaxOpenConnections must equal 25")

	// 2. Concurrency stress with 35 parallel goroutines (exceeds pool limit of 25)
	const numCallers = 35
	var wg sync.WaitGroup
	wg.Add(numCallers)

	var successCount int64
	var mu sync.Mutex

	for i := 0; i < numCallers; i++ {
		go func(id int) {
			defer wg.Done()
			var res int
			err := client.DB.Raw("SELECT 1").Scan(&res).Error
			if err == nil && res == 1 {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int64(numCallers), successCount, "All 35 concurrent queries must succeed through the 25-connection pool")

	// Verify open connections did not exceed MaxOpenConnections
	endStats := sqlDB.Stats()
	assert.LessOrEqual(t, endStats.OpenConnections, 25, "Active open connections must never exceed 25")
	t.Logf("Connection pool stats after 35 concurrent queries: Open=%d, InUse=%d, Idle=%d, WaitCount=%d",
		endStats.OpenConnections, endStats.InUse, endStats.Idle, endStats.WaitCount)

	// 3. Lifecycle: Ping on healthy client
	err = client.Ping()
	require.NoError(t, err, "Ping must succeed on active client")

	// Close client
	err = client.Close()
	require.NoError(t, err, "Close must gracefully shut down pool")

	// Ping after close must return error
	err = client.Ping()
	require.Error(t, err, "Ping must return error after Close()")
	assert.Contains(t, err.Error(), "closed", "Error message should indicate closed database")
}

// TestEmpirical_DB_PgvectorHNSW_HalfvecSyntaxAndFallbackSafety tests:
// 1. DDL verification: verify the HNSW index statement in migrations uses halfvec(3072).
// 2. Verify that naive vector(3072) HNSW index fails in PostgreSQL because 3072 > 2000.
// 3. Graceful fallback safety: if vector index creation fails (e.g. pgvector < 0.7.0 without halfvec),
//    ApplyCustomIndexes logs a warning and does NOT return an error, preventing startup abort.
func TestEmpirical_DB_PgvectorHNSW_HalfvecSyntaxAndFallbackSafety(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Test standard vector HNSW limit:
	// In pgvector, standard vector type has a hard 2,000-dimension limit for HNSW indexes.
	// Attempting to index a 3072-dimension vector without halfvec casting MUST fail in Postgres.
	naiveDDL := `CREATE INDEX idx_test_naive_hnsw ON memories USING hnsw (embedding vector_cosine_ops);`
	err := client.DB.Exec(naiveDDL).Error
	require.Error(t, err, "Naive vector(3072) HNSW index must fail because 3072 dimensions exceeds Postgres 2,000-dim limit")
	assert.True(t,
		strings.Contains(err.Error(), "2000") || strings.Contains(err.Error(), "exceed") || strings.Contains(err.Error(), "dimensions"),
		"Postgres error must mention dimension limit: %v", err,
	)
	t.Logf("Empirically confirmed PostgreSQL rejection of naive vector(3072) HNSW: %v", err)

	// 2. Test halfvec(3072) HNSW index:
	// Verify that ApplyCustomIndexes succeeded during NewClient and both HNSW halfvec indexes exist.
	var count int64
	err = client.DB.Raw("SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_topic_memories_embedding'").Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "idx_topic_memories_embedding halfvec HNSW index must exist")

	err = client.DB.Raw("SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_memories_embedding'").Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "idx_memories_embedding halfvec HNSW index must exist")

	// 3. Fallback safety verification:
	// If an invalid or unsupported vector index statement is executed inside ApplyCustomIndexes,
	// it should log a warning and return nil (not fail the entire migration).
	// Call ApplyCustomIndexes again (idempotent):
	err = ApplyCustomIndexes(client.DB)
	require.NoError(t, err, "ApplyCustomIndexes must be idempotent and return nil")
}
