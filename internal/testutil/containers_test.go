package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTestDB(t *testing.T) {
	client, cleanup := NewTestDB(t)
	defer cleanup()

	require.NotNil(t, client)
	err := client.Ping()
	require.NoError(t, err)

	// Verify pgvector extension exists
	var count int64
	err = client.DB.Raw("SELECT count(*) FROM pg_extension WHERE extname = 'vector'").Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "pgvector extension must exist")
}

func TestNewTestRedis(t *testing.T) {
	rdb, cleanup := NewTestRedis(t)
	defer cleanup()

	require.NotNil(t, rdb)
	ctx := context.Background()

	err := rdb.Set(ctx, "test_key", "test_val", 10*time.Second).Err()
	require.NoError(t, err)

	val, err := rdb.Get(ctx, "test_key").Result()
	require.NoError(t, err)
	assert.Equal(t, "test_val", val)
}
