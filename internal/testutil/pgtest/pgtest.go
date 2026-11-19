// Package pgtest is the single source of throwaway PostgreSQL databases for tests (BL-11).
//
// One container is started per test binary (package) on first use; every NewDatabase call creates
// a fresh, empty database inside it and drops it when the test ends. Tests therefore never share
// state with each other and can never reach the compose (live) database.
//
// It deliberately does not import internal/db, so internal/db's own tests can use it too;
// everything else should call testutil.NewTestDB, which adds the migrated *db.Client.
// Only _test.go files (and testutil) may import it, so testcontainers never links into a service.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Image is the exact image the compose postgres service runs (BL-7), so tests exercise the same
// Postgres and pgvector versions as production. TestImageMatchesCompose keeps the two in sync.
const Image = "pgvector/pgvector:0.8.6-pg15@sha256:a947c45cdc5906a1bc951f20a8709e321256343ee0f251e4ae00b5e7def4e6da"

var (
	// mu serializes container start-up and CREATE/DROP DATABASE: concurrent CREATE DATABASE
	// statements copying template1 fail with "source database is being accessed by other users".
	mu       sync.Mutex
	adminDSN string
	adminDB  *gorm.DB
	// pgContainer is the shared container, kept so tests can run the Postgres client tools that
	// ship inside it (see ToolRunner).
	pgContainer *postgres.PostgresContainer
)

// AdminDSN starts the shared container if needed and returns its admin connection string.
func AdminDSN(ctx context.Context) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	return startLocked(ctx)
}

func startLocked(ctx context.Context) (string, error) {
	if adminDB != nil {
		return adminDSN, nil
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dsn, db, err := run(ctx)
		if err == nil {
			adminDSN, adminDB = dsn, db
			return adminDSN, nil
		}
		lastErr = fmt.Errorf("attempt %d/3: %w", attempt, err)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	return "", lastErr
}

func run(ctx context.Context) (string, *gorm.DB, error) {
	container, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("user"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return "", nil, fmt.Errorf("start postgres container: %w", err)
	}
	// On success the container is reaped by testcontainers' Ryuk sidecar when the binary exits.
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return "", nil, fmt.Errorf("postgres connection string: %w", err)
	}
	db, err := gorm.Open(postgresDriver.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		_ = container.Terminate(ctx)
		return "", nil, fmt.Errorf("connect admin db: %w", err)
	}
	pgContainer = container
	return dsn, db, nil
}

// NewDatabase creates an isolated, empty database and returns its DSN; it is dropped on test
// cleanup. Docker is required: the test fails (never silently skips) when it is unavailable,
// because a skipped integration test looks exactly like a passing one.
func NewDatabase(t testing.TB) string {
	t.Helper()
	name := "testdb_" + strings.ReplaceAll(uuid.New().String(), "-", "")

	mu.Lock()
	dsn, err := startLocked(context.Background())
	if err == nil {
		err = adminDB.Exec("CREATE DATABASE " + name).Error
	}
	mu.Unlock()
	if err != nil {
		t.Fatalf("pgtest: create database %s: %v", name, err)
	}
	t.Cleanup(func() { _ = drop(name) })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("pgtest: parse admin DSN: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// NewDatabaseOwnedBy creates an isolated, empty database owned by another superuser role (created
// on first use) and returns a DSN that connects as that role. It stands in for a different
// machine, where the database role has another name: a dump taken under one role must restore
// under the other (V4-43).
func NewDatabaseOwnedBy(t testing.TB, role string) string {
	t.Helper()
	if strings.Trim(role, "abcdefghijklmnopqrstuvwxyz_") != "" {
		t.Fatalf("pgtest: role name %q must be lower-case letters and underscores", role)
	}
	name := "testdb_" + strings.ReplaceAll(uuid.New().String(), "-", "")

	mu.Lock()
	dsn, err := startLocked(context.Background())
	if err == nil {
		err = adminDB.Exec("DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '" + role + "') THEN " +
			"CREATE ROLE " + role + " SUPERUSER LOGIN PASSWORD 'password'; END IF; END $$").Error
	}
	if err == nil {
		err = adminDB.Exec("CREATE DATABASE " + name + " OWNER " + role).Error
	}
	mu.Unlock()
	if err != nil {
		t.Fatalf("pgtest: create database %s owned by %s: %v", name, role, err)
	}
	t.Cleanup(func() { _ = drop(name) })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("pgtest: parse admin DSN: %v", err)
	}
	u.User = url.UserPassword(role, "password")
	u.Path = "/" + name
	return u.String()
}

// DropDatabase drops a database returned by NewDatabase right away (it is idempotent, so the
// automatic drop at test end stays harmless). Callers use it to free a database before the test ends.
func DropDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("pgtest: parse DSN: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(name, "testdb_") {
		return fmt.Errorf("pgtest: refusing to drop %q: not a pgtest database", name)
	}
	return drop(name)
}

func drop(name string) error {
	mu.Lock()
	defer mu.Unlock()
	if adminDB == nil {
		return nil
	}
	return adminDB.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error
}
