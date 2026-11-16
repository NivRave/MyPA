package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/testutil/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	redisMu        sync.Mutex
	redisContainer testcontainers.Container
	redisAddr      string
)

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

// NewTestDB creates a completely isolated PostgreSQL database for the test (pgtest, BL-11),
// executes all GORM migrations and custom indexes, and returns a connected *db.Client and a cleanup
// func that closes and drops it (it is also dropped automatically when the test ends).
func NewTestDB(t *testing.T) (*db.Client, func()) {
	t.Helper()
	dsn := pgtest.NewDatabase(t)
	client, err := db.NewClientAutoMigrate(dsn)
	if err != nil {
		t.Fatalf("failed to create db client: %v", err)
	}
	return client, func() {
		_ = client.Close()
		_ = pgtest.DropDatabase(dsn)
	}
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

// GetPostgresDSN returns the admin connection string of the shared Postgres test container.
func GetPostgresDSN(ctx context.Context) (string, error) {
	return pgtest.AdminDSN(ctx)
}

// GetRedisAddr returns the network address (host:port) of the singleton Redis container.
func GetRedisAddr(ctx context.Context) (string, error) {
	return getSingletonRedis(ctx)
}
