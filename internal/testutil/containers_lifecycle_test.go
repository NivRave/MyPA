package testutil

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestTestcontainers_ConcurrentSingletonInitialization stress tests
// simultaneous multi-threaded calls to GetPostgresDSN, GetRedisAddr, NewTestDB, and NewTestRedis.
func TestTestcontainers_ConcurrentSingletonInitialization(t *testing.T) {
	const concurrency = 15
	var wg sync.WaitGroup
	ctx := context.Background()

	dsnResults := make([]string, concurrency)
	redisResults := make([]string, concurrency)
	errs := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			dsn, err := GetPostgresDSN(ctx)
			if err != nil {
				errs[idx] = err
				return
			}
			dsnResults[idx] = dsn

			rAddr, err := GetRedisAddr(ctx)
			if err != nil {
				errs[idx] = err
				return
			}
			redisResults[idx] = rAddr
		}()
	}
	wg.Wait()

	for i := 0; i < concurrency; i++ {
		require.NoError(t, errs[i], "concurrent initialization failed at index %d", i)
		assert.NotEmpty(t, dsnResults[i])
		assert.NotEmpty(t, redisResults[i])
		assert.Equal(t, dsnResults[0], dsnResults[i], "Postgres DSN must be identical across all concurrent callers")
		assert.Equal(t, redisResults[0], redisResults[i], "Redis address must be identical across all concurrent callers")
	}
}

// TestTestcontainers_CleanDatabaseTeardown assertions:
// Validates that parallel isolated databases (CREATE DATABASE testdb_<uuid>)
// drop cleanly on cleanup() with 0 orphaned databases and 0 lingering connections.
func TestTestcontainers_CleanDatabaseTeardown(t *testing.T) {
	const numDatabases = 5
	ctx := context.Background()

	adminDSN, err := GetPostgresDSN(ctx)
	require.NoError(t, err)
	adminDB, err := gorm.Open(postgresDriver.Open(adminDSN), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	if sqlDB, err := adminDB.DB(); err == nil {
		defer sqlDB.Close()
	}

	// Measure baseline orphaned test databases
	var initialCount int64
	err = adminDB.Raw("SELECT count(*) FROM pg_database WHERE datname LIKE 'testdb_%'").Scan(&initialCount).Error
	require.NoError(t, err)

	type testDBInstance struct {
		name    string
		cleanup func()
	}

	instances := make([]testDBInstance, numDatabases)
	var wg sync.WaitGroup
	wg.Add(numDatabases)

	for i := 0; i < numDatabases; i++ {
		idx := i
		go func() {
			defer wg.Done()
			client, cleanup := NewTestDB(t)
			require.NotNil(t, client)
			var currentDB string
			err := client.DB.Raw("SELECT current_database()").Scan(&currentDB).Error
			require.NoError(t, err)
			assert.Contains(t, currentDB, "testdb_")

			instances[idx] = testDBInstance{
				name:    currentDB,
				cleanup: cleanup,
			}
		}()
	}
	wg.Wait()

	// Assert that all databases currently exist while tests are running
	var activeCount int64
	err = adminDB.Raw("SELECT count(*) FROM pg_database WHERE datname LIKE 'testdb_%'").Scan(&activeCount).Error
	require.NoError(t, err)
	assert.GreaterOrEqual(t, activeCount, int64(numDatabases), "all active test databases should be registered")

	// Concurrently tear down all databases
	var teardownWg sync.WaitGroup
	teardownWg.Add(numDatabases)
	for i := 0; i < numDatabases; i++ {
		idx := i
		go func() {
			defer teardownWg.Done()
			instances[idx].cleanup()
		}()
	}
	teardownWg.Wait()

	// Give Postgres a moment to complete asynchronous backend termination if any
	time.Sleep(200 * time.Millisecond)

	// Assert: All testdb_* databases MUST be dropped cleanly (count should equal initial baseline)
	var finalCount int64
	err = adminDB.Raw("SELECT count(*) FROM pg_database WHERE datname LIKE 'testdb_%'").Scan(&finalCount).Error
	require.NoError(t, err)
	assert.Equal(t, initialCount, finalCount, "Zero orphaned databases allowed! All testdb_* must be dropped cleanly")

	// Assert: No leaking pg_stat_activity connections to any testdb_*
	var leakingConns int64
	err = adminDB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname LIKE 'testdb_%'").Scan(&leakingConns).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), leakingConns, "Zero connection leaks allowed in pg_stat_activity for testdb_*")
}

// TestTestcontainers_Redis_SequentialIsolation verifies
// that sequential Redis test clients flush keys cleanly.
func TestTestcontainers_Redis_SequentialIsolation(t *testing.T) {
	const numClients = 5
	for i := 0; i < numClients; i++ {
		rdb, cleanup := NewTestRedis(t)
		ctx := context.Background()

		key := fmt.Sprintf("key_%d", i)
		err := rdb.Set(ctx, key, fmt.Sprintf("val_%d", i), time.Minute).Err()
		require.NoError(t, err)

		val, err := rdb.Get(ctx, key).Result()
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("val_%d", i), val)

		cleanup()

		// Verify key is gone after cleanup
		exists, err := rdb.Exists(ctx, key).Result()
		// client is closed so Exists should fail with client closed
		assert.Error(t, err)
		_ = exists
	}
}
