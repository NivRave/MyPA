package pgtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestImageMatchesCompose(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "..", "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(compose), "image: "+Image, "pgtest.Image must be the compose postgres image")
}

func TestNewDatabase_IsolatedAndEmpty(t *testing.T) {
	open := func(dsn string) *gorm.DB {
		db, err := gorm.Open(postgresDriver.Open(dsn), &gorm.Config{Logger: logger.Discard})
		require.NoError(t, err)
		return db
	}
	a, b := NewDatabase(t), NewDatabase(t)
	require.NotEqual(t, a, b)

	dbA, dbB := open(a), open(b)
	require.NoError(t, dbA.Exec("CREATE TABLE marker (id int)").Error)

	var n int64
	require.NoError(t, dbB.Raw("SELECT count(*) FROM information_schema.tables WHERE table_name = 'marker'").Scan(&n).Error)
	assert.Zero(t, n, "a table created in one test database must not exist in another")

	var vector int64
	require.NoError(t, dbA.Raw("SELECT count(*) FROM pg_available_extensions WHERE name = 'vector'").Scan(&vector).Error)
	assert.Equal(t, int64(1), vector, "pgvector must be available, as in production")
}
