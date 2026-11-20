package graph

import (
	"context"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockBabyRepo struct {
	db.BabyRepository
	createCareLogCalled bool
	lastCreatedLog      *models.BabyCareLog
}

func (m *mockBabyRepo) CreateCareLog(s identity.Scope, log *models.BabyCareLog) error {
	stampScope(s, &log.HouseholdID, &log.FamilyGroup)
	m.createCareLogCalled = true
	log.ID = 101
	m.lastCreatedLog = log
	return nil
}

type mockMemoryRepo struct {
	db.MemoryV2Repository
	savedMemories []*models.TopicMemory
	deletedIDs    []uint
	pinnedIDs     []uint
	mockMem       *models.TopicMemory
}

func (m *mockMemoryRepo) SaveTopicMemory(ctx context.Context, s identity.Scope, memory *models.TopicMemory) error {
	stampScope(s, &memory.HouseholdID, &memory.FamilyGroup)
	memory.ID = 202
	m.savedMemories = append(m.savedMemories, memory)
	return nil
}

func (m *mockMemoryRepo) GetTopicMemory(ctx context.Context, s identity.Scope, id uint) (*models.TopicMemory, error) {
	if m.mockMem != nil && m.mockMem.ID == id {
		return m.mockMem, nil
	}
	return &models.TopicMemory{
		ID:          id,
		Topic:       "health",
		Subtopic:    "test",
		Fact:        "Test fact",
		FamilyGroup: "fam-test",
	}, nil
}

func (m *mockMemoryRepo) DeleteTopicMemory(ctx context.Context, s identity.Scope, id uint) error {
	m.deletedIDs = append(m.deletedIDs, id)
	return nil
}

func (m *mockMemoryRepo) PinTopicMemory(ctx context.Context, s identity.Scope, id uint, isPinned bool) (*models.TopicMemory, error) {
	m.pinnedIDs = append(m.pinnedIDs, id)
	return &models.TopicMemory{
		ID:       id,
		Topic:    "health",
		Fact:     "Test fact",
		IsPinned: isPinned,
	}, nil
}

func (m *mockMemoryRepo) ListTopicMemories(ctx context.Context, s identity.Scope, topic string, includeSuperseded bool) ([]models.TopicMemory, error) {
	return []models.TopicMemory{
		{ID: 1, Topic: "health", Fact: "Fact 1", FamilyGroup: s.LegacyLabel(), HouseholdID: s.HouseholdID()},
	}, nil
}

func (m *mockMemoryRepo) SearchTopicMemories(ctx context.Context, s identity.Scope, topic, query string, tags []string) ([]models.TopicMemory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) SearchSimilarMemories(ctx context.Context, s identity.Scope, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	return nil, nil
}
func (m *mockMemoryRepo) SupersedeMemory(ctx context.Context, s identity.Scope, oldID uint, newID uint) error {
	return nil
}
func (m *mockMemoryRepo) UpsertEntityProfile(ctx context.Context, s identity.Scope, profile *models.EntityProfile) error {
	stampScope(s, &profile.HouseholdID, &profile.FamilyGroup)
	profile.ID = 303
	return nil
}
func (m *mockMemoryRepo) GetEntityProfile(ctx context.Context, s identity.Scope, topic, entityName string) (*models.EntityProfile, error) {
	return nil, nil
}
func (m *mockMemoryRepo) ListEntityProfiles(ctx context.Context, s identity.Scope, topic string) ([]models.EntityProfile, error) {
	return nil, nil
}
func (m *mockMemoryRepo) ListActiveMemoriesForConsolidation(ctx context.Context) ([]models.TopicMemory, error) {
	return nil, nil
}

type mockAutoRepo struct {
	db.AutomationRepository
	createdAPIs  []*models.ExternalAPIConnector
	createdRules []*models.AutomationRule
}

func (m *mockAutoRepo) CreateAPIConnector(s identity.Scope, connector *models.ExternalAPIConnector) error {
	stampScope(s, &connector.HouseholdID, &connector.FamilyGroup)
	connector.ID = 404
	m.createdAPIs = append(m.createdAPIs, connector)
	return nil
}

func (m *mockAutoRepo) CreateAutomationRule(s identity.Scope, rule *models.AutomationRule) error {
	stampScope(s, &rule.HouseholdID, &rule.FamilyGroup)
	rule.ID = 505
	m.createdRules = append(m.createdRules, rule)
	return nil
}

func TestResolver_InvalidID_Validation(t *testing.T) {
	resolver := NewResolver(ResolverConfig{})
	ctx := context.Background()

	// 1. DeleteTopicMemory invalid ID
	_, err := resolver.Mutation().DeleteTopicMemory(ctx, "invalid-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid topic memory id")

	_, err = resolver.Mutation().DeleteTopicMemory(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id cannot be empty")

	// 2. PinTopicMemory invalid ID
	_, err = resolver.Mutation().PinTopicMemory(ctx, "0", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid topic memory id")

	// 3. CompleteHomeMaintenance invalid ID
	_, err = resolver.Mutation().CompleteHomeMaintenance(ctx, "-10")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid home maintenance id")

	// 4. DeleteAutomationRule invalid ID
	_, err = resolver.Mutation().DeleteAutomationRule(ctx, "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid automation rule id")
}

func TestResolver_PositiveAuthorization(t *testing.T) {
	memRepo := &mockMemoryRepo{}
	resolver := NewResolver(ResolverConfig{
		MemoryRepo: memRepo,
		Households: labelStore("FamilyA", "FamilyB", "default"),
	})

	// V4-35: no caller can name a household. A member of FamilyA writes into FamilyA, and an owner
	// (an admin before V4-35, who could pick any family) writes into their own household only.
	ctxNonAdmin := signedIn(t, resolver, "FamilyA", "usr-1", "member")
	res, err := resolver.Mutation().SaveTopicMemory(ctxNonAdmin, "health", "diet", "No nuts", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "FamilyA", res.FamilyGroup)

	ctxAdmin := signedIn(t, resolver, "default", "admin-1", "admin")
	res, err = resolver.Mutation().SaveTopicMemory(ctxAdmin, "health", "diet", "No nuts", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "default", res.FamilyGroup, "never FamilyB")
	for _, m := range memRepo.savedMemories {
		assert.NotEqual(t, identitytest.HouseholdID("FamilyB"), m.HouseholdID)
	}
}

func TestResolver_Mutations_EmitEvents(t *testing.T) {
	bus := events.NewEventBus()
	var publishedEvents []events.MutationEvent
	bus.Subscribe(func(ctx context.Context, ev events.MutationEvent) error {
		publishedEvents = append(publishedEvents, ev)
		return nil
	})

	babyRepo := &mockBabyRepo{}
	memRepo := &mockMemoryRepo{}
	autoRepo := &mockAutoRepo{}

	resolver := NewResolver(ResolverConfig{
		EventBus:       bus,
		BabyRepo:       babyRepo,
		MemoryRepo:     memRepo,
		AutomationRepo: autoRepo,
		Households:     labelStore("fam-test"),
	})

	ctx := signedIn(t, resolver, "fam-test", "usr-100", "member")

	// 1. LogBabyCare mutation
	val := 150.0
	unit := "ml"
	bLog, err := resolver.Mutation().LogBabyCare(ctx, "1", "feeding", &val, &unit, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, bLog)
	assert.True(t, babyRepo.createCareLogCalled)

	// 2. SaveTopicMemory mutation
	fact := "Prefers oat milk"
	mLog, err := resolver.Mutation().SaveTopicMemory(ctx, "diet", "milk", fact, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, mLog)
	require.Len(t, memRepo.savedMemories, 1)

	// 3. RegisterExternalAPI mutation (fixes BUG-03)
	method := "GET"
	apiLog, err := resolver.Mutation().RegisterExternalAPI(ctx, "WeatherAPI", "https://93.184.216.34/weather", &method, nil)
	require.NoError(t, err)
	require.NotNil(t, apiLog)
	require.Len(t, autoRepo.createdAPIs, 1)
	assert.Equal(t, "fam-test", autoRepo.createdAPIs[0].FamilyGroup)

	// 4. SaveAutomationRule mutation (fixes BUG-03)
	ruleLog, err := resolver.Mutation().SaveAutomationRule(ctx, "BabyRule", "mutation", "baby", "{}", "notify partner", nil)
	require.NoError(t, err)
	require.NotNil(t, ruleLog)
	require.Len(t, autoRepo.createdRules, 1)
	assert.Equal(t, "fam-test", autoRepo.createdRules[0].FamilyGroup)

	// Allow brief async event queue processing if any
	time.Sleep(50 * time.Millisecond)

	require.Len(t, publishedEvents, 4)
	assert.Equal(t, "baby", publishedEvents[0].Topic)
	assert.Equal(t, "create", publishedEvents[0].Action)
	assert.Equal(t, "baby_care_log", publishedEvents[0].EntityType)

	assert.Equal(t, "diet", publishedEvents[1].Topic)
	assert.Equal(t, "create", publishedEvents[1].Action)
	assert.Equal(t, "topic_memory", publishedEvents[1].EntityType)

	assert.Equal(t, "automation", publishedEvents[2].Topic)
	assert.Equal(t, "create", publishedEvents[2].Action)
	assert.Equal(t, "external_api_connector", publishedEvents[2].EntityType)
	assert.Equal(t, "fam-test", publishedEvents[2].FamilyGroup)

	assert.Equal(t, "automation", publishedEvents[3].Topic)
	assert.Equal(t, "create", publishedEvents[3].Action)
	assert.Equal(t, "automation_rule", publishedEvents[3].EntityType)
	assert.Equal(t, "fam-test", publishedEvents[3].FamilyGroup)
}
