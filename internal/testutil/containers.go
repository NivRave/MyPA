package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	pgMu        sync.Mutex
	pgContainer *postgres.PostgresContainer
	pgAdminDSN  string
	pgAdminDB   *gorm.DB

	redisMu        sync.Mutex
	redisContainer testcontainers.Container
	redisAddr      string
)

// getSingletonPostgres initializes and returns the shared PostgreSQL container and admin GORM DB.
func getSingletonPostgres(ctx context.Context) (string, *gorm.DB, error) {
	pgMu.Lock()
	defer pgMu.Unlock()

	if pgContainer != nil && pgAdminDB != nil {
		return pgAdminDSN, pgAdminDB, nil
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		dbName := "testdb"
		dbUser := "user"
		dbPassword := "password"

		container, err := postgres.Run(ctx,
			"pgvector/pgvector:pg16",
			postgres.WithDatabase(dbName),
			postgres.WithUsername(dbUser),
			postgres.WithPassword(dbPassword),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			lastErr = fmt.Errorf("failed to start postgres container (attempt %d/3): %w", attempt, err)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		connStr, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			lastErr = fmt.Errorf("failed to get connection string: %w", err)
			_ = container.Terminate(ctx)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		adminDB, err := gorm.Open(postgresDriver.Open(connStr), &gorm.Config{})
		if err != nil {
			lastErr = fmt.Errorf("failed to connect admin gorm db: %w", err)
			_ = container.Terminate(ctx)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		pgContainer = container
		pgAdminDSN = connStr
		pgAdminDB = adminDB
		slog.Info("testutil: singleton postgres container ready", "connStr", connStr)
		return pgAdminDSN, pgAdminDB, nil
	}

	return "", nil, lastErr
}

// getSingletonRedis initializes and returns the shared Redis container address.
func getSingletonRedis(ctx context.Context) (string, error) {
	redisMu.Lock()
	defer redisMu.Unlock()

	if redisContainer != nil && redisAddr != "" {
		return redisAddr, nil
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req := testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "redis:7-alpine",
				ExposedPorts: []string{"6379/tcp"},
				WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
			},
			Started: true,
		}

		container, err := testcontainers.GenericContainer(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("failed to start redis container (attempt %d/3): %w", attempt, err)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		host, err := container.Host(ctx)
		if err != nil {
			lastErr = fmt.Errorf("failed to get redis host: %w", err)
			_ = container.Terminate(ctx)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		mappedPort, err := container.MappedPort(ctx, "6379/tcp")
		if err != nil {
			lastErr = fmt.Errorf("failed to get redis mapped port: %w", err)
			_ = container.Terminate(ctx)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		redisContainer = container
		redisAddr = fmt.Sprintf("%s:%s", host, mappedPort.Port())
		slog.Info("testutil: singleton redis container ready", "addr", redisAddr)
		return redisAddr, nil
	}

	return "", lastErr
}

// NewTestDB creates a completely isolated PostgreSQL database for the test,
// executes all GORM migrations and custom indexes, and returns a connected *db.Client and a cleanup func.
func NewTestDB(t *testing.T) (*db.Client, func()) {
	t.Helper()
	ctx := context.Background()

	adminDSN, adminDB, err := getSingletonPostgres(ctx)
	if err != nil {
		t.Fatalf("failed to initialize postgres testcontainer: %v", err)
	}

	uniqueID := strings.ReplaceAll(uuid.New().String(), "-", "")
	dbName := fmt.Sprintf("testdb_%s", uniqueID)

	pgMu.Lock(); createErr := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName)).Error; pgMu.Unlock(); if err := createErr; err != nil {
		t.Fatalf("failed to create isolated test database %s: %v", dbName, err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		_ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		t.Fatalf("failed to parse admin DSN: %v", err)
	}
	u.Path = "/" + dbName
	testDSN := u.String()

	client, err := db.NewClientAutoMigrate(testDSN)
	if err != nil {
		_ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		t.Fatalf("failed to create db client for %s: %v", dbName, err)
	}

	cleanup := func() {
		if client != nil {
			_ = client.Close()
		}
		if adminDB != nil {
			pgMu.Lock(); _ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName)); pgMu.Unlock()
		}
	}

	return client, cleanup
}

// NewTestRedis creates and returns a connected Redis client to the shared Redis container,
// along with a cleanup function that flushes the DB and closes the client.
func NewTestRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	ctx := context.Background()

	addr, err := getSingletonRedis(ctx)
	if err != nil {
		t.Fatalf("failed to initialize redis testcontainer: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("failed to ping test redis: %v", err)
	}

	cleanup := func() {
		_ = rdb.FlushDB(context.Background())
		_ = rdb.Close()
	}

	return rdb, cleanup
}

// GetPostgresDSN returns the admin connection string of the singleton Postgres container.
func GetPostgresDSN(ctx context.Context) (string, error) {
	dsn, _, err := getSingletonPostgres(ctx)
	return dsn, err
}

// GetRedisAddr returns the network address (host:port) of the singleton Redis container.
func GetRedisAddr(ctx context.Context) (string, error) {
	return getSingletonRedis(ctx)
}
