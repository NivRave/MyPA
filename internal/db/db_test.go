package db

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	testPgOnce      sync.Once
	testPgContainer *postgres.PostgresContainer
	testPgAdminDSN  string
	testPgAdminDB   *gorm.DB
	testPgErr       error
)

func getTestSingletonPostgres(ctx context.Context) (string, *gorm.DB, error) {
	testPgOnce.Do(func() {
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
			testPgErr = fmt.Errorf("failed to start postgres container: %w", err)
			return
		}

		connStr, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			testPgErr = fmt.Errorf("failed to get connection string: %w", err)
			return
		}

		adminDB, err := gorm.Open(postgresDriver.Open(connStr), &gorm.Config{})
		if err != nil {
			testPgErr = fmt.Errorf("failed to connect admin gorm db: %w", err)
			return
		}

		testPgContainer = container
		testPgAdminDSN = connStr
		testPgAdminDB = adminDB
	})

	return testPgAdminDSN, testPgAdminDB, testPgErr
}

func setupTestDB(t *testing.T) (*Client, func()) {
	t.Helper()
	ctx := context.Background()

	adminDSN, adminDB, err := getTestSingletonPostgres(ctx)
	if err != nil {
		t.Fatalf("failed to initialize postgres testcontainer: %v", err)
	}

	uniqueID := strings.ReplaceAll(uuid.New().String(), "-", "")
	dbName := fmt.Sprintf("testdb_%s", uniqueID)

	if err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName)).Error; err != nil {
		t.Fatalf("failed to create isolated test database %s: %v", dbName, err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		_ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		t.Fatalf("failed to parse admin DSN: %v", err)
	}
	u.Path = "/" + dbName
	testDSN := u.String()

	client, err := NewClient(testDSN)
	if err != nil {
		_ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		t.Fatalf("failed to create db client for %s: %v", dbName, err)
	}

	cleanup := func() {
		if client != nil {
			_ = client.Close()
		}
		if adminDB != nil {
			_ = adminDB.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		}
	}

	return client, cleanup
}

func TestNewClient_EmptyDSN(t *testing.T) {
	_, err := NewClient("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database URL is empty")
}

func TestAuditSessions(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	userID := "user-123"

	session1 := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        userID,
		StartTime:     time.Now().Add(-1 * time.Hour),
		EndTime:       time.Now(),
		UserPrompt:    "Prompt 1",
		FinalResponse: "Response 1",
	}
	err := client.InsertAuditSession(session1)
	require.NoError(t, err)

	session2 := models.AuditSession{
		ID:            uuid.New().String(),
		UserID:        userID,
		StartTime:     time.Now(),
		EndTime:       time.Now().Add(1 * time.Hour),
		UserPrompt:    "Prompt 2",
		FinalResponse: "Response 2",
	}
	err = client.InsertAuditSession(session2)
	require.NoError(t, err)

	last, err := client.GetLastAuditSessionForUser(userID)
	require.NoError(t, err)
	assert.Equal(t, "Prompt 2", last.UserPrompt)

	users, err := client.GetUniqueUsers()
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, userID, users[0])
}

func TestAuditEvents(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	event := models.AuditEvent{
		ID:              uuid.New().String(),
		SessionID:       uuid.New().String(),
		EventType:       "TEST_EVENT",
		ActionName:      "test action",
		RequestPayload:  "{}",
		ResponsePayload: "{}",
		StartTime:       time.Now(),
		EndTime:         time.Now().Add(time.Second),
	}

	err := client.InsertAuditEvent(event)
	require.NoError(t, err)
}

func TestMemories(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	userID := "user-456"

	vec1 := make([]float32, 3072)
	vec1[0] = 1.0
	
	mem1 := models.Memory{
		UserID:    userID,
		Fact:      "I love apples",
		Embedding: pgvector.NewVector(vec1),
		CreatedAt: time.Now(),
	}
	err := client.SaveMemory(mem1)
	require.NoError(t, err)

	vec2 := make([]float32, 3072)
	vec2[1] = 1.0

	mem2 := models.Memory{
		UserID:    userID,
		Fact:      "I love bananas",
		Embedding: pgvector.NewVector(vec2),
		CreatedAt: time.Now(),
	}
	err = client.SaveMemory(mem2)
	require.NoError(t, err)

	searchVec := make([]float32, 3072)
	searchVec[0] = 0.9

	results, err := client.SearchMemories([]string{userID}, pgvector.NewVector(searchVec), 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "I love apples", results[0].Fact)
}

func TestReminders(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	rem1 := models.ScheduledReminder{
		UserID:  "user-rem",
		Message: "Past reminder",
		DueTime: time.Now().Add(-1 * time.Hour),
		IsSent:  false,
	}
	err := client.SaveReminder(rem1)
	require.NoError(t, err)

	rem2 := models.ScheduledReminder{
		UserID:  "user-rem",
		Message: "Future reminder",
		DueTime: time.Now().Add(1 * time.Hour),
		IsSent:  false,
	}
	err = client.SaveReminder(rem2)
	require.NoError(t, err)

	due, err := client.GetDueReminders()
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "Past reminder", due[0].Message)

	err = client.MarkReminderSent(due[0].ID)
	require.NoError(t, err)

	dueAgain, err := client.GetDueReminders()
	require.NoError(t, err)
	assert.Empty(t, dueAgain)
}

func TestTopicMemories_VectorSearchAndFallback(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	user := "user-vec-test"
	fam := "fam-vec-test"

	// 1. Insert TopicMemory with vector
	v1 := make([]float32, 3072)
	v1[0] = 1.0
	tm1 := models.TopicMemory{
		UserID:      user,
		FamilyGroup: fam,
		Topic:       "baby",
		Subtopic:    "nutrition",
		Fact:        "Baby drinks 150ml milk formula",
		Scope:       "family",
		ValidFrom:   time.Now().Add(-1 * time.Hour),
		Embedding:   pgvector.NewVector(v1),
	}
	err := client.SaveTopicMemory(ctx, &tm1)
	require.NoError(t, err)

	v2 := make([]float32, 3072)
	v2[1] = 1.0
	tm2 := models.TopicMemory{
		UserID:      user,
		FamilyGroup: fam,
		Topic:       "house",
		Subtopic:    "cleaning",
		Fact:        "HVAC filters changed quarterly",
		Scope:       "family",
		ValidFrom:   time.Now().Add(-1 * time.Hour),
		Embedding:   pgvector.NewVector(v2),
	}
	err = client.SaveTopicMemory(ctx, &tm2)
	require.NoError(t, err)

	// Search closest to v1
	queryVec := make([]float32, 3072)
	queryVec[0] = 0.95
	results, err := client.SearchSimilarMemories(ctx, user, fam, pgvector.NewVector(queryVec), 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Baby drinks 150ml milk formula", results[0].Fact)
	assert.Equal(t, "baby", results[0].Topic)

	// 2. Test fallback to legacy memories when 0 topic memories match
	legacyUser := "user-legacy-only"
	legacyVec := make([]float32, 3072)
	legacyVec[2] = 1.0
	legacyMem := models.Memory{
		UserID:    legacyUser,
		Fact:      "Grandma's birthday is on June 12",
		Scope:     "personal",
		Embedding: pgvector.NewVector(legacyVec),
		CreatedAt: time.Now(),
	}
	err = client.SaveMemory(legacyMem)
	require.NoError(t, err)

	queryLegacy := make([]float32, 3072)
	queryLegacy[2] = 0.9
	fbResults, err := client.SearchSimilarMemories(ctx, legacyUser, "", pgvector.NewVector(queryLegacy), 1)
	require.NoError(t, err)
	require.Len(t, fbResults, 1)
	assert.Equal(t, "Grandma's birthday is on June 12", fbResults[0].Fact)
	assert.Equal(t, "general", fbResults[0].Topic)
}

func TestSharedReminders_DueAndSent(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	pastDue := time.Now().Add(-10 * time.Minute)
	futureDue := time.Now().Add(10 * time.Minute)

	rem1 := models.SharedTaskReminder{
		FamilyGroup:     "fam-sr-test",
		Topic:           "baby",
		Title:           "Buy diapers",
		DueTime:         &pastDue,
		CreatedByUserID: "mom-1",
		IsCompleted:     false,
		IsSent:          false,
		CreatedAt:       time.Now(),
	}
	err := client.CreateSharedReminder(&rem1)
	require.NoError(t, err)

	rem2 := models.SharedTaskReminder{
		FamilyGroup:     "fam-sr-test",
		Topic:           "baby",
		Title:           "Doctor visit",
		DueTime:         &futureDue,
		CreatedByUserID: "mom-1",
		IsCompleted:     false,
		IsSent:          false,
		CreatedAt:       time.Now(),
	}
	err = client.CreateSharedReminder(&rem2)
	require.NoError(t, err)

	due, err := client.GetDueSharedReminders()
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "Buy diapers", due[0].Title)

	err = client.MarkSharedReminderSent(due[0].ID)
	require.NoError(t, err)

	dueAfter, err := client.GetDueSharedReminders()
	require.NoError(t, err)
	assert.Empty(t, dueAfter)
}

func TestDB_ConnectionPoolSizing(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	sqlDB, err := client.DB.DB()
	require.NoError(t, err)

	stats := sqlDB.Stats()
	assert.Equal(t, 25, stats.MaxOpenConnections, "MaxOpenConnections must be configured to 25")
}

func TestDB_CustomIndexesApplied(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	expectedIndexes := []string{
		"idx_audit_sessions_start_time",
		"idx_audit_sessions_user_start",
		"idx_mutation_logs_family_created",
		"idx_mutation_logs_user_created",
		"idx_financial_transactions_family_date",
		"idx_baby_care_logs_family_timestamp",
		"idx_schedule_events_family_start",
		"idx_shared_task_reminders_due_completed",
	}

	for _, idxName := range expectedIndexes {
		var count int64
		err := client.DB.Raw(
			"SELECT count(*) FROM pg_indexes WHERE indexname = ?", idxName,
		).Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "Index %s must exist in PostgreSQL", idxName)
	}
}

func TestDB_PingAndClose(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()

	err := client.Ping()
	require.NoError(t, err, "Ping must succeed on healthy connection")

	err = client.Close()
	require.NoError(t, err, "Close must succeed on connection pool")
}


