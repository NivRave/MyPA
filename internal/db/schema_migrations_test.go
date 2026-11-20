package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaFingerprint lists every column (with type, nullability, default) and every index
// definition in the public schema, excluding goose's own bookkeeping table.
func schemaFingerprint(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	var out []string
	rows, err := sqlDB.Query(`
		SELECT table_name, column_name, data_type, udt_name, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`)
	require.NoError(t, err)
	for rows.Next() {
		var tbl, col, dt, udt, nullable, def string
		require.NoError(t, rows.Scan(&tbl, &col, &dt, &udt, &nullable, &def))
		out = append(out, fmt.Sprintf("col %s.%s %s/%s null=%s default=%s", tbl, col, dt, udt, nullable, def))
	}
	require.NoError(t, rows.Close())

	rows, err = sqlDB.Query(`
		SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	require.NoError(t, err)
	for rows.Next() {
		var name, def string
		require.NoError(t, rows.Scan(&name, &def))
		out = append(out, "idx "+name+" "+def)
	}
	require.NoError(t, rows.Close())
	sort.Strings(out)
	return out
}

func openRaw(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	c, err := Open(dsn)
	require.NoError(t, err)
	sqlDB, err := c.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return sqlDB
}

// P4-3 drift guard: a fresh database built by goose migrations must be identical to one built by
// AutoMigrate from the Go models. A model change without a migration fails here.
func TestMigrations_FreshDatabaseMatchesModels(t *testing.T) {
	migratedDSN := newEmptyTestDatabase(t)
	migrated := openRaw(t, migratedDSN)
	require.NoError(t, Migrate(context.Background(), migrated))

	autoDSN := newEmptyTestDatabase(t)
	autoClient, err := NewClientAutoMigrate(autoDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = autoClient.Close() })
	auto, err := autoClient.DB.DB()
	require.NoError(t, err)

	got, want := schemaFingerprint(t, migrated), schemaFingerprint(t, auto)
	missing, extra := diff(want, got), diff(got, want)
	assert.Empty(t, missing, "in models but not produced by migrations — add a migration")
	assert.Empty(t, extra, "produced by migrations but not in models")
	assert.Greater(t, len(got), 100, "sanity: full schema")
}

func diff(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

// An existing pre-goose database (the pre-goose AutoMigrate schema, with data) is adopted at the
// baseline without re-running it, and later migrations (legacy memories, P2-4; households, V4-30)
// still apply. The pre-goose schema is the baseline itself (00001 was generated from it), so the
// database is built from it and goose's bookkeeping is removed; today's models would already
// contain the later migrations' tables.
func TestMigrations_AdoptsExistingDatabase(t *testing.T) {
	dsn := newEmptyTestDatabase(t)
	legacy := openRaw(t, dsn)
	migrateTo(t, legacy, 1)
	_, err := legacy.Exec(`DROP TABLE goose_db_version`)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO users (platform_id, name, family_group) VALUES ('u1', 'U', 'fam')`)
	require.NoError(t, err)
	_, err = legacy.Exec(`INSERT INTO memories (user_id, scope, fact, embedding, created_at)
		VALUES ('u1', 'personal', 'adopted fact', array_fill(0, ARRAY[3072])::vector, now())`)
	require.NoError(t, err)
	// Simulate a pre-P2-4 database: the fact exists only in the legacy table.
	_, err = legacy.Exec(`DELETE FROM topic_memories`)
	require.NoError(t, err)

	sqlDB := openRaw(t, dsn)
	require.NoError(t, Migrate(context.Background(), sqlDB), "adoption must not try to re-create existing tables")

	var n int
	require.NoError(t, sqlDB.QueryRow(`SELECT count(*) FROM topic_memories WHERE fact = 'adopted fact'`).Scan(&n))
	assert.Equal(t, 1, n, "migration 2 (legacy memories) ran on the adopted database")
	pending, err := HasPendingMigrations(context.Background(), sqlDB)
	require.NoError(t, err)
	assert.False(t, pending)
}

func TestMigrations_Idempotent(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	require.NoError(t, Migrate(context.Background(), sqlDB))
	require.NoError(t, Migrate(context.Background(), sqlDB))
}

// The services refuse to run against a database with pending migrations.
func TestEnsureMigrated(t *testing.T) {
	sqlDB := openRaw(t, newEmptyTestDatabase(t))
	err := EnsureMigrated(context.Background(), sqlDB)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "-migrate-only"), "error must tell the operator how to migrate: %v", err)

	require.NoError(t, Migrate(context.Background(), sqlDB))
	assert.NoError(t, EnsureMigrated(context.Background(), sqlDB))
}
