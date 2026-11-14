package durability

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryDurabilityRepo is a fully functional in-memory implementation of Repository for unit tests.
type inMemoryDurabilityRepo struct {
	mu           sync.RWMutex
	credentials  map[string]models.EncryptedCredential // key: userID + ":" + provider
	mutationLogs []models.MutationLog
	idempotency  map[string]models.IdempotencyRecord
}

func newInMemoryDurabilityRepo() *inMemoryDurabilityRepo {
	return &inMemoryDurabilityRepo{
		credentials:  make(map[string]models.EncryptedCredential),
		mutationLogs: make([]models.MutationLog, 0),
		idempotency:  make(map[string]models.IdempotencyRecord),
	}
}

func (r *inMemoryDurabilityRepo) SaveEncryptedCredential(cred models.EncryptedCredential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := cred.UserID + ":" + cred.Provider
	if cred.UpdatedAt.IsZero() {
		cred.UpdatedAt = time.Now()
	}
	r.credentials[key] = cred
	return nil
}

func (r *inMemoryDurabilityRepo) GetEncryptedCredential(userID, provider string) (*models.EncryptedCredential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := userID + ":" + provider
	cred, ok := r.credentials[key]
	if !ok {
		return nil, errors.New("record not found")
	}
	return &cred, nil
}

func (r *inMemoryDurabilityRepo) InsertMutationLog(log models.MutationLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	r.mutationLogs = append(r.mutationLogs, log)
	return nil
}

func (r *inMemoryDurabilityRepo) GetMutationLogsByTopic(familyGroup, userID, topic string, limit int) ([]models.MutationLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	var res []models.MutationLog
	// return newest first
	for i := len(r.mutationLogs) - 1; i >= 0; i-- {
		item := r.mutationLogs[i]
		if familyGroup != "" || userID != "" {
			matchesFamily := (familyGroup != "" && item.FamilyGroup == familyGroup)
			matchesUser := (userID != "" && item.UserID == userID)
			if !matchesFamily && !matchesUser {
				continue
			}
		}
		if topic == "" || strings.EqualFold(topic, "all") || strings.EqualFold(item.Topic, topic) {
			res = append(res, item)
			if len(res) >= limit {
				break
			}
		}
	}
	return res, nil
}

func (r *inMemoryDurabilityRepo) GetMutationLogByID(id string) (*models.MutationLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.mutationLogs {
		if l.ID == id {
			return &l, nil
		}
	}
	return nil, errors.New("mutation log not found")
}

func (r *inMemoryDurabilityRepo) GetLastMutation(userID string, familyGroup string) (*models.MutationLog, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := len(r.mutationLogs) - 1; i >= 0; i-- {
		item := r.mutationLogs[i]
		if item.IsUndone {
			continue
		}
		matchesUser := (userID != "" && item.UserID == userID)
		matchesFamily := (familyGroup != "" && item.FamilyGroup == familyGroup)
		if (matchesUser || matchesFamily) || (userID == "" && familyGroup == "") {
			return &item, nil
		}
	}
	return nil, errors.New("no active mutations found")
}

func (r *inMemoryDurabilityRepo) MarkMutationUndone(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range r.mutationLogs {
		if l.ID == id {
			r.mutationLogs[i].IsUndone = true
			return nil
		}
	}
	return errors.New("mutation log not found")
}

func (r *inMemoryDurabilityRepo) RollbackSnapshot(ctx context.Context, log models.MutationLog, tableName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range r.mutationLogs {
		if l.ID == log.ID {
			r.mutationLogs[i].IsUndone = true
			return nil
		}
	}
	return nil
}

func (r *inMemoryDurabilityRepo) CheckIdempotency(key string) (*models.IdempotencyRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.idempotency[key]
	if !ok {
		return nil, errors.New("idempotency record not found")
	}
	if time.Now().After(rec.ExpiresAt) {
		return nil, errors.New("idempotency record expired")
	}
	return &rec, nil
}

func (r *inMemoryDurabilityRepo) SaveIdempotency(rec models.IdempotencyRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.ExpiresAt.IsZero() {
		rec.ExpiresAt = time.Now().Add(24 * time.Hour)
	}
	r.idempotency[rec.Key] = rec
	return nil
}

func (r *inMemoryDurabilityRepo) ExportTopicData(familyGroup, userID, topic string, format string, limit int) (string, error) {
	logs, err := r.GetMutationLogsByTopic(familyGroup, userID, topic, limit)
	if err != nil {
		return "", err
	}

	normFormat := strings.ToLower(strings.TrimSpace(format))
	if normFormat == "csv" {
		var buf bytes.Buffer
		writer := csv.NewWriter(&buf)
		header := []string{"id", "timestamp", "user_id", "family_group", "topic", "action", "entity_type", "entity_id", "summary", "is_undone"}
		if err := writer.Write(header); err != nil {
			return "", err
		}
		for _, item := range logs {
			row := []string{
				item.ID,
				item.CreatedAt.Format(time.RFC3339),
				item.UserID,
				item.FamilyGroup,
				item.Topic,
				item.Action,
				item.EntityType,
				item.EntityID,
				item.Summary,
				fmt.Sprintf("%t", item.IsUndone),
			}
			if err := writer.Write(row); err != nil {
				return "", err
			}
		}
		writer.Flush()
		return buf.String(), writer.Error()
	}

	bytesData, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytesData), nil
}

// 1. Test Model Registration in db.RegisterModel
func TestModelRegistration(t *testing.T) {
	modelsList := db.GetRegisteredModels()
	assert.NotEmpty(t, modelsList, "registered models should not be empty")

	foundCred := false
	foundMutation := false
	foundIdempotency := false

	for _, m := range modelsList {
		switch m.(type) {
		case *models.EncryptedCredential:
			foundCred = true
		case *models.MutationLog:
			foundMutation = true
		case *models.IdempotencyRecord:
			foundIdempotency = true
		}
	}

	assert.True(t, foundCred, "EncryptedCredential model must be registered")
	assert.True(t, foundMutation, "MutationLog model must be registered")
	assert.True(t, foundIdempotency, "IdempotencyRecord model must be registered")
}

// 2. Test Module Interface Compliance
func TestModuleInterfaceCompliance(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	mod := NewModule(repo)

	var _ orchestrator.ToolModule = mod
	assert.Equal(t, "durability", mod.Name())

	decls := mod.Declarations()
	require.Len(t, decls, 3)

	names := []string{decls[0].Name, decls[1].Name, decls[2].Name}
	assert.Contains(t, names, "undo_last_action")
	assert.Contains(t, names, "list_recent_changes")
	assert.Contains(t, names, "export_topic_data")

	systemPrompt := mod.SystemPromptSection(context.Background(), orchestrator.CallContext{})
	assert.Contains(t, systemPrompt, "Durability")
	assert.Contains(t, systemPrompt, "undo_last_action")
}

// 3. Test Encrypted Credential Fallback (AES-256-GCM)
func TestEncryptedCredentialFallback(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	mod := NewModule(repo)
	ctx := context.Background()

	originalToken := []byte(`{"access_token":"ya29.sample_oauth_token","refresh_token":"1//sample_refresh","expiry":"2026-11-04T00:00:00Z"}`)
	userID := "user_123"
	provider := "google_oauth"
	passphrase := "my-secure-session-key"

	// Save credential fallback
	err := mod.SaveCredentialFallback(ctx, userID, provider, originalToken, passphrase)
	require.NoError(t, err)

	// Verify directly from repository that ciphertext is encrypted
	cred, err := repo.GetEncryptedCredential(userID, provider)
	require.NoError(t, err)
	assert.Equal(t, userID, cred.UserID)
	assert.Equal(t, provider, cred.Provider)
	assert.NotEqual(t, originalToken, cred.Ciphertext, "Ciphertext in DB must NOT be plaintext")

	// Retrieve and decrypt credential fallback
	decrypted, err := mod.GetCredentialFallback(ctx, userID, provider, passphrase)
	require.NoError(t, err)
	assert.Equal(t, originalToken, decrypted, "Decrypted data must match original token")

	// Attempt decrypt with wrong passphrase must fail
	_, err = mod.GetCredentialFallback(ctx, userID, provider, "wrong-passphrase")
	assert.Error(t, err, "Decryption with incorrect passphrase must fail")
}

// 4. Test EventBus Subscriber Logging
func TestEventBusSubscriber(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	bus := events.NewEventBus()
	_ = NewModule(repo, bus)
	ctx := context.Background()

	ev := events.MutationEvent{
		ID:          "mut-001",
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		EntityID:    "log-99",
		ActorUserID: "sarah_mom",
		FamilyGroup: "TheSmiths",
		BeforeJSON:  "",
		AfterJSON:   `{"feeding_ml":120,"type":"formula"}`,
		Summary:     "Fed 120ml formula to Emma",
		Timestamp:   time.Now(),
	}

	err := bus.Publish(ctx, ev)
	require.NoError(t, err)

	// Verify that the event was persisted into repo
	logs, err := repo.GetMutationLogsByTopic("fam-cooper", "sarah_mom", "baby", 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)

	saved := logs[0]
	assert.Equal(t, "mut-001", saved.ID)
	assert.Equal(t, "baby", saved.Topic)
	assert.Equal(t, "create", saved.Action)
	assert.Equal(t, "BabyCareLog", saved.EntityType)
	assert.Equal(t, "log-99", saved.EntityID)
	assert.Equal(t, "sarah_mom", saved.UserID)
	assert.Equal(t, "TheSmiths", saved.FamilyGroup)
	assert.Equal(t, "Fed 120ml formula to Emma", saved.Summary)
	assert.False(t, saved.IsUndone)
}

// 5. Test Undo Last Action Tool & Generic Rollback
func TestUndoLastAction_Generic(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus)
	ctx := context.Background()

	user := &models.User{
		PlatformID:  "user_dad",
		FamilyGroup: "TheSmiths",
	}
	callCtx := orchestrator.CallContext{
		User:     user,
		EventBus: bus,
	}

	// 1. Try undo when no mutations exist
	resp, err := mod.Execute(ctx, callCtx, "undo_last_action", nil)
	require.NoError(t, err)
	assert.Contains(t, resp, "No recent undoable actions found")

	// 2. Publish a mutation
	ev := events.MutationEvent{
		ID:          "mut-002",
		Topic:       "finance",
		Action:      "create",
		EntityType:  "FinancialTransaction",
		EntityID:    "tx-456",
		ActorUserID: "user_dad",
		FamilyGroup: "TheSmiths",
		BeforeJSON:  "",
		AfterJSON:   `{"amount":85,"merchant":"Supermarket"}`,
		Summary:     "Logged 85 NIS groceries at Supermarket",
		Timestamp:   time.Now(),
	}
	require.NoError(t, bus.Publish(ctx, ev))

	// Verify it's active
	last, err := repo.GetLastMutation("user_dad", "TheSmiths")
	require.NoError(t, err)
	assert.Equal(t, "mut-002", last.ID)
	assert.False(t, last.IsUndone)

	// Track undo events published
	var undoEvents []events.MutationEvent
	bus.Subscribe(func(ctx context.Context, e events.MutationEvent) error {
		if e.Action == "undo" {
			undoEvents = append(undoEvents, e)
		}
		return nil
	})

	// 3. Execute undo_last_action
	resp, err = mod.Execute(ctx, callCtx, "undo_last_action", map[string]interface{}{
		"reason": "accidentally duplicated receipt",
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "Successfully undone")
	assert.Contains(t, resp, "FinancialTransaction")

	// 4. Verify mutation is marked undone in repo
	log, err := repo.GetMutationLogByID("mut-002")
	require.NoError(t, err)
	assert.True(t, log.IsUndone, "Mutation should be marked undone")

	// 5. Verify undo event was published
	assert.NotEmpty(t, undoEvents, "Undo event should be published to event bus")
	assert.Equal(t, "undo", undoEvents[0].Action)

	// 6. Calling undo again should report no active action
	resp, err = mod.Execute(ctx, callCtx, "undo_last_action", nil)
	require.NoError(t, err)
	assert.Contains(t, resp, "No recent undoable actions found")
}

// 6. Test Custom UndoHandler Registration
func TestCustomUndoHandler(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	mod := NewModule(repo)
	ctx := context.Background()

	customHandlerInvoked := false
	mod.RegisterUndoHandler("BabyCareLog", func(ctx context.Context, log models.MutationLog, callCtx orchestrator.CallContext) (string, error) {
		customHandlerInvoked = true
		return fmt.Sprintf("Custom rollback: Deleted baby care log record %s", log.EntityID), nil
	})

	// Insert mutation
	err := repo.InsertMutationLog(models.MutationLog{
		ID:          "mut-003",
		UserID:      "sarah_mom",
		FamilyGroup: "TheSmiths",
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		EntityID:    "log-789",
		Summary:     "Logged temperature 38.5 C",
		IsUndone:    false,
		CreatedAt:   time.Now(),
	})
	require.NoError(t, err)

	callCtx := orchestrator.CallContext{
		User: &models.User{PlatformID: "sarah_mom", FamilyGroup: "TheSmiths"},
	}

	resp, err := mod.Execute(ctx, callCtx, "undo_last_action", nil)
	require.NoError(t, err)
	assert.True(t, customHandlerInvoked, "Custom undo handler must be invoked for BabyCareLog")
	assert.Contains(t, resp, "Custom rollback: Deleted baby care log record log-789")
}

// 7. Test List Recent Changes Tool
func TestListRecentChanges(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := orchestrator.CallContext{}

	// Insert multiple logs across topics
	_ = repo.InsertMutationLog(models.MutationLog{
		ID:         "mut-1",
		Topic:      "baby",
		Action:     "create",
		EntityType: "Feeding",
		Summary:    "Morning feeding 150ml",
		CreatedAt:  time.Now().Add(-10 * time.Minute),
	})
	_ = repo.InsertMutationLog(models.MutationLog{
		ID:         "mut-2",
		Topic:      "house",
		Action:     "update",
		EntityType: "Maintenance",
		Summary:    "AC filter replaced",
		CreatedAt:  time.Now().Add(-5 * time.Minute),
	})
	_ = repo.InsertMutationLog(models.MutationLog{
		ID:         "mut-3",
		Topic:      "finance",
		Action:     "create",
		EntityType: "Expense",
		Summary:    "Grocery 200 NIS",
		CreatedAt:  time.Now(),
	})

	// 1. Query all topics
	out, err := mod.Execute(ctx, callCtx, "list_recent_changes", map[string]interface{}{
		"topic": "all",
		"limit": 10,
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Morning feeding 150ml")
	assert.Contains(t, out, "AC filter replaced")
	assert.Contains(t, out, "Grocery 200 NIS")

	// 2. Query filtered topic
	out, err = mod.Execute(ctx, callCtx, "list_recent_changes", map[string]interface{}{
		"topic": "baby",
		"limit": 5,
	})
	require.NoError(t, err)
	assert.Contains(t, out, "Morning feeding 150ml")
	assert.NotContains(t, out, "AC filter replaced")

	// 3. Query non-existent topic
	out, err = mod.Execute(ctx, callCtx, "list_recent_changes", map[string]interface{}{
		"topic": "schedule",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "No recent changes recorded for topic \"schedule\"")
}

// 8. Test Export Topic Data (JSON and CSV)
func TestExportTopicData(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	mod := NewModule(repo)
	ctx := context.Background()
	callCtx := orchestrator.CallContext{}

	_ = repo.InsertMutationLog(models.MutationLog{
		ID:          "exp-1",
		UserID:      "u1",
		FamilyGroup: "F1",
		Topic:       "house",
		Action:      "create",
		EntityType:  "LeaseContract",
		EntityID:    "c-100",
		Summary:     "Signed apartment lease",
		CreatedAt:   time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
	})

	// 1. Export as JSON
	jsonOut, err := mod.Execute(ctx, callCtx, "export_topic_data", map[string]interface{}{
		"topic":  "house",
		"format": "json",
	})
	require.NoError(t, err)

	var parsed []models.MutationLog
	err = json.Unmarshal([]byte(jsonOut), &parsed)
	require.NoError(t, err, "Exported JSON must be valid")
	require.Len(t, parsed, 1)
	assert.Equal(t, "exp-1", parsed[0].ID)
	assert.Equal(t, "house", parsed[0].Topic)
	assert.Equal(t, "Signed apartment lease", parsed[0].Summary)

	// 2. Export as CSV
	csvOut, err := mod.Execute(ctx, callCtx, "export_topic_data", map[string]interface{}{
		"topic":  "house",
		"format": "csv",
	})
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(csvOut))
	records, err := reader.ReadAll()
	require.NoError(t, err, "Exported CSV must be valid RFC 4180")
	require.Len(t, records, 2, "Expected 1 header row + 1 data row")

	assert.Equal(t, "id", records[0][0])
	assert.Equal(t, "exp-1", records[1][0])
	assert.Equal(t, "house", records[1][4])
	assert.Equal(t, "Signed apartment lease", records[1][8])
}

// 9. Test Idempotency Repository Operations
func TestIdempotencyOperations(t *testing.T) {
	repo := newInMemoryDurabilityRepo()

	key := "idem-webhook-event-12345"
	rec := models.IdempotencyRecord{
		Key:             key,
		UserID:          "user_x",
		ToolName:        "create_calendar_event",
		ResponseSummary: "Created event 'Dr Appointment'",
		ExpiresAt:       time.Now().Add(1 * time.Hour),
	}

	// Save idempotency
	err := repo.SaveIdempotency(rec)
	require.NoError(t, err)

	// Check idempotency before expiration
	found, err := repo.CheckIdempotency(key)
	require.NoError(t, err)
	assert.Equal(t, key, found.Key)
	assert.Equal(t, "Created event 'Dr Appointment'", found.ResponseSummary)

	// Expired record
	expiredKey := "idem-expired-999"
	err = repo.SaveIdempotency(models.IdempotencyRecord{
		Key:       expiredKey,
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})
	require.NoError(t, err)

	_, err = repo.CheckIdempotency(expiredKey)
	assert.Error(t, err, "Expired idempotency record must not be returned as valid")
}

// TestSEC04_CrossTenant_DurabilityUndo_Blocked empirically verifies that durability mutations
// cannot be undone across tenants by specifying an arbitrary mutation_id, and confirms non-regression.
func TestSEC04_CrossTenant_DurabilityUndo_Blocked(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus)
	ctx := context.Background()

	// 1. Seed mutation log for Family Alpha
	alphaMut := models.MutationLog{
		ID:          "mut-alpha-99",
		Topic:       "finance",
		Action:      "create",
		EntityType:  "FinancialTransaction",
		EntityID:    "tx-99",
		UserID:      "user_alpha",
		FamilyGroup: "family_alpha",
		Summary:     "Alpha Grocery Expense",
		IsUndone:    false,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, repo.InsertMutationLog(alphaMut))

	// 2. Family Beta attempts to undo Family Alpha's mutation by passing mutation_id
	callCtxBeta := orchestrator.CallContext{
		User:    &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta"},
		Message: models.Message{UserID: "user_beta"},
	}

	_, undoBetaErr := mod.Execute(ctx, callCtxBeta, "undo_last_action", map[string]interface{}{
		"mutation_id": "mut-alpha-99",
	})
	assert.Error(t, undoBetaErr, "Family Beta must NOT be able to undo Family Alpha's mutation")
	assert.Contains(t, undoBetaErr.Error(), "permission denied")

	// Verify mutation remains undone == false
	log1, err := repo.GetMutationLogByID("mut-alpha-99")
	require.NoError(t, err)
	assert.False(t, log1.IsUndone, "Mutation must not be marked undone after unauthorized attempt")

	// 3. Legitimate same-family undo (non-regression)
	callCtxAlpha := orchestrator.CallContext{
		User:    &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha"},
		Message: models.Message{UserID: "user_alpha"},
	}

	undoAlphaResp, err := mod.Execute(ctx, callCtxAlpha, "undo_last_action", map[string]interface{}{
		"mutation_id": "mut-alpha-99",
	})
	require.NoError(t, err, "Family Alpha must be able to undo their own mutation")
	assert.Contains(t, undoAlphaResp, "Successfully undone")

	// Verify mutation is now marked undone
	log1After, err := repo.GetMutationLogByID("mut-alpha-99")
	require.NoError(t, err)
	assert.True(t, log1After.IsUndone, "Mutation must be marked undone after legitimate undo")
}

func TestRollbackEngine_AllowlistAndIdempotency(t *testing.T) {
	repo := newInMemoryDurabilityRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus)
	ctx := context.Background()

	// 1. Rejection of system entity
	sysMut := models.MutationLog{
		ID:          "mut-sys-1",
		Topic:       "system",
		Action:      "update",
		EntityType:  "users",
		EntityID:    "user_1",
		UserID:      "user_1",
		FamilyGroup: "family_1",
	}
	require.NoError(t, repo.InsertMutationLog(sysMut))

	_, err := mod.Rollback(ctx, "user_1", "family_1", "mut-sys-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "system entity")

	// 2. Rejection of unknown entity
	unkMut := models.MutationLog{
		ID:          "mut-unk-1",
		Topic:       "custom",
		Action:      "delete",
		EntityType:  "arbitrary_table",
		EntityID:    "123",
		UserID:      "user_1",
		FamilyGroup: "family_1",
	}
	require.NoError(t, repo.InsertMutationLog(unkMut))

	_, err = mod.Rollback(ctx, "user_1", "family_1", "mut-unk-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowlisted")

	// 3. Valid allowlisted entity rollback and idempotency
	validMut := models.MutationLog{
		ID:          "mut-valid-1",
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		EntityID:    "log-42",
		UserID:      "user_1",
		FamilyGroup: "family_1",
		Summary:     "Logged feeding",
	}
	require.NoError(t, repo.InsertMutationLog(validMut))

	res, err := mod.Rollback(ctx, "user_1", "family_1", "mut-valid-1")
	require.NoError(t, err)
	assert.False(t, res.AlreadyUndone)
	assert.Contains(t, res.Detail, "Reverted")

	// Second rollback is idempotent
	res2, err := mod.Rollback(ctx, "user_1", "family_1", "mut-valid-1")
	require.NoError(t, err)
	assert.True(t, res2.AlreadyUndone)
	assert.Contains(t, res2.Detail, "already been undone")
}


