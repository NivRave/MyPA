package shared

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemorySharedRepo implements Repository for unit testing.
type inMemorySharedRepo struct {
	mu        sync.RWMutex
	spaces    map[string]*models.SharedSpace         // key: familyGroup + ":" + topic
	reminders map[uint]*models.SharedTaskReminder
	users     map[string][]models.User               // key: familyGroup
	nextRemID uint
}

func newInMemoryRepo() *inMemorySharedRepo {
	return &inMemorySharedRepo{
		spaces:    make(map[string]*models.SharedSpace),
		reminders: make(map[uint]*models.SharedTaskReminder),
		users:     make(map[string][]models.User),
		nextRemID: 1,
	}
}

func (r *inMemorySharedRepo) UpsertSharedSpace(space *models.SharedSpace) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.ToLower(space.FamilyGroup) + ":" + strings.ToLower(space.Topic)
	if existing, exists := r.spaces[key]; exists {
		existing.CanView = space.CanView
		existing.CanEdit = space.CanEdit
		existing.NotifyMode = space.NotifyMode
		existing.OwnerUserID = space.OwnerUserID
		existing.SharedWithUserID = space.SharedWithUserID
		existing.UpdatedAt = time.Now()
		space.ID = existing.ID
		space.UpdatedAt = existing.UpdatedAt
	} else {
		space.ID = uint(len(r.spaces) + 1)
		space.UpdatedAt = time.Now()
		cp := *space
		r.spaces[key] = &cp
	}
	return nil
}

func (r *inMemorySharedRepo) GetSharedSpacesForFamily(familyGroup string) ([]models.SharedSpace, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.SharedSpace
	fgLower := strings.ToLower(familyGroup)
	for _, sp := range r.spaces {
		if strings.ToLower(sp.FamilyGroup) == fgLower {
			result = append(result, *sp)
		}
	}
	return result, nil
}

func (r *inMemorySharedRepo) GetSharedSpace(familyGroup, topic string) (*models.SharedSpace, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := strings.ToLower(familyGroup) + ":" + strings.ToLower(topic)
	if sp, ok := r.spaces[key]; ok {
		cp := *sp
		return &cp, nil
	}
	return nil, nil
}

func (r *inMemorySharedRepo) CreateSharedReminder(reminder *models.SharedTaskReminder) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	reminder.ID = r.nextRemID
	r.nextRemID++
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = time.Now()
	}
	cp := *reminder
	r.reminders[reminder.ID] = &cp
	return nil
}

func (r *inMemorySharedRepo) CompleteSharedReminder(id uint, completedByUserID string, familyGroup ...string) (*models.SharedTaskReminder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rem, exists := r.reminders[id]
	if !exists {
		return nil, fmt.Errorf("shared reminder with id %d not found", id)
	}

	if len(familyGroup) > 0 && familyGroup[0] != "" {
		if rem.FamilyGroup != familyGroup[0] && rem.CreatedByUserID != completedByUserID && rem.AssignedToUserID != completedByUserID {
			return nil, fmt.Errorf("shared reminder with id %d not found in family %q", id, familyGroup[0])
		}
	}

	now := time.Now()
	rem.IsCompleted = true
	rem.CompletedByUserID = completedByUserID
	rem.CompletedAt = &now
	cp := *rem
	return &cp, nil
}

func (r *inMemorySharedRepo) ListSharedReminders(familyGroup string, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []models.SharedTaskReminder
	fgLower := strings.ToLower(familyGroup)
	topicLower := strings.ToLower(strings.TrimSpace(topic))

	for _, rem := range r.reminders {
		if strings.ToLower(rem.FamilyGroup) != fgLower {
			continue
		}
		if topicLower != "" && topicLower != "*" && strings.ToLower(rem.Topic) != topicLower {
			continue
		}
		if !includeCompleted && rem.IsCompleted {
			continue
		}
		result = append(result, *rem)
	}
	return result, nil
}

func (r *inMemorySharedRepo) GetUsersByFamilyGroup(familyGroup string) ([]models.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.users[strings.ToLower(familyGroup)], nil
}

func (r *inMemorySharedRepo) setUsers(familyGroup string, users []models.User) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[strings.ToLower(familyGroup)] = users
}

// ------------------- Tests -------------------

func TestModels_SharedTopicDefaults(t *testing.T) {
	assert.True(t, models.IsDefaultSharedTopic("baby"))
	assert.True(t, models.IsDefaultSharedTopic("House"))
	assert.True(t, models.IsDefaultSharedTopic("finance"))
	assert.True(t, models.IsDefaultSharedTopic("family"))
	assert.False(t, models.IsDefaultSharedTopic("personal"))
	assert.False(t, models.IsDefaultSharedTopic("work"))
	assert.False(t, models.IsDefaultSharedTopic("random_custom"))

	assert.Equal(t, models.NotifyModeInstant, models.DefaultNotifyModeForTopic("baby"))
	assert.Equal(t, models.NotifyModeInstant, models.DefaultNotifyModeForTopic("house"))
	assert.Equal(t, models.NotifyModeSilent, models.DefaultNotifyModeForTopic("personal"))
}

func TestRepository_CRUD(t *testing.T) {
	repo := newInMemoryRepo()

	// 1. Upsert Shared Space
	sp := &models.SharedSpace{
		FamilyGroup: "TheSmiths",
		Topic:       "baby",
		OwnerUserID: "sarah-1",
		CanView:     true,
		CanEdit:     true,
		NotifyMode:  models.NotifyModeInstant,
	}
	err := repo.UpsertSharedSpace(sp)
	require.NoError(t, err)
	assert.NotZero(t, sp.ID)

	// Retrieve
	found, err := repo.GetSharedSpace("TheSmiths", "baby")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "baby", found.Topic)
	assert.Equal(t, models.NotifyModeInstant, found.NotifyMode)

	// Update existing space
	sp.NotifyMode = models.NotifyModeSilent
	err = repo.UpsertSharedSpace(sp)
	require.NoError(t, err)

	updated, err := repo.GetSharedSpace("TheSmiths", "baby")
	require.NoError(t, err)
	assert.Equal(t, models.NotifyModeSilent, updated.NotifyMode)

	// List spaces for family
	spaces, err := repo.GetSharedSpacesForFamily("TheSmiths")
	require.NoError(t, err)
	assert.Len(t, spaces, 1)

	// 2. Create and complete Shared Reminder
	due := time.Now().Add(2 * time.Hour)
	rem := &models.SharedTaskReminder{
		FamilyGroup:      "TheSmiths",
		Topic:            "baby",
		Title:            "Give Vitamin D drops",
		DueTime:          &due,
		CreatedByUserID:  "sarah-1",
		AssignedToUserID: "john-2",
	}
	err = repo.CreateSharedReminder(rem)
	require.NoError(t, err)
	assert.Equal(t, uint(1), rem.ID)

	// List pending
	list, err := repo.ListSharedReminders("TheSmiths", "baby", false)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.False(t, list[0].IsCompleted)

	// Complete reminder
	completed, err := repo.CompleteSharedReminder(rem.ID, "john-2")
	require.NoError(t, err)
	assert.True(t, completed.IsCompleted)
	assert.Equal(t, "john-2", completed.CompletedByUserID)
	assert.NotNil(t, completed.CompletedAt)

	// List pending (should be empty now)
	pendingList, err := repo.ListSharedReminders("TheSmiths", "baby", false)
	require.NoError(t, err)
	assert.Empty(t, pendingList)

	// List with includeCompleted = true
	allList, err := repo.ListSharedReminders("TheSmiths", "baby", true)
	require.NoError(t, err)
	assert.Len(t, allList, 1)
}

func TestNotifier_FormatNotification(t *testing.T) {
	// Log baby care
	ev1 := events.MutationEvent{
		Topic:      "baby",
		Action:     "create",
		EntityType: "BabyCareLog",
		Summary:    "Feeding 120ml formula",
	}
	msg1 := FormatNotification(ev1, "Sarah")
	assert.Equal(t, "📢 [Shared • Baby] Sarah logged: Feeding 120ml formula", msg1)

	// Complete task
	ev2 := events.MutationEvent{
		Topic:      "house",
		Action:     "complete",
		EntityType: "SharedTaskReminder",
		Summary:    "Replaced AC filter",
	}
	msg2 := FormatNotification(ev2, "Alex")
	assert.Equal(t, "📢 [Shared • House] Alex completed: Replaced AC filter", msg2)

	// Update record
	ev3 := events.MutationEvent{
		Topic:      "finance",
		Action:     "update",
		EntityType: "CategoryBudget",
		Summary:    "Monthly grocery budget set to 3000 NIS",
	}
	msg3 := FormatNotification(ev3, "Sarah")
	assert.Equal(t, "📢 [Shared • Finance] Sarah updated: Monthly grocery budget set to 3000 NIS", msg3)
}

func TestNotifier_CrossUserRouting(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("TheSmiths", []models.User{
		{ID: "user-sarah", PlatformID: "tg-100", Name: "Sarah", FamilyGroup: "TheSmiths"},
		{ID: "user-john", PlatformID: "tg-200", Name: "John", FamilyGroup: "TheSmiths"},
		{ID: "user-baby", PlatformID: "", Name: "Leo", FamilyGroup: "TheSmiths"},
	})

	var notifiedUsers []string
	var notifiedMessages []string
	var mu sync.Mutex

	mockSender := func(ctx context.Context, recipientID, text string) error {
		mu.Lock()
		defer mu.Unlock()
		notifiedUsers = append(notifiedUsers, recipientID)
		notifiedMessages = append(notifiedMessages, text)
		return nil
	}

	bus := events.NewEventBus()
	notifier := NewNotifier(repo, mockSender)
	notifier.RegisterWithBus(bus)

	ctx := context.Background()

	// Sarah logs feeding for baby (default shared topic)
	ev := events.MutationEvent{
		Topic:       "baby",
		Action:      "create",
		EntityType:  "BabyCareLog",
		ActorUserID: "user-sarah",
		FamilyGroup: "TheSmiths",
		Summary:     "Feeding 120ml formula",
	}

	err := bus.Publish(ctx, ev)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	// Only John should be notified on tg-200
	require.Len(t, notifiedUsers, 1)
	assert.Equal(t, "tg-200", notifiedUsers[0])
	assert.Contains(t, notifiedMessages[0], "Sarah logged: Feeding 120ml formula")
	assert.Contains(t, notifiedMessages[0], "[Shared • Baby]")
}

func TestNotifier_SilentAndPrivateTopics(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("TheSmiths", []models.User{
		{ID: "user-sarah", PlatformID: "tg-100", Name: "Sarah", FamilyGroup: "TheSmiths"},
		{ID: "user-john", PlatformID: "tg-200", Name: "John", FamilyGroup: "TheSmiths"},
	})

	var notifiedUsers []string
	mockSender := func(ctx context.Context, recipientID, text string) error {
		notifiedUsers = append(notifiedUsers, recipientID)
		return nil
	}

	bus := events.NewEventBus()
	notifier := NewNotifier(repo, mockSender)
	notifier.RegisterWithBus(bus)
	ctx := context.Background()

	// 1. Personal topic (not shared by default)
	evPrivate := events.MutationEvent{
		Topic:       "personal",
		Action:      "create",
		ActorUserID: "user-sarah",
		FamilyGroup: "TheSmiths",
		Summary:     "Private diary entry",
	}
	err := bus.Publish(ctx, evPrivate)
	require.NoError(t, err)
	assert.Empty(t, notifiedUsers)

	// 2. Baby topic configured explicitly with NotifyMode = "silent"
	err = repo.UpsertSharedSpace(&models.SharedSpace{
		FamilyGroup: "TheSmiths",
		Topic:       "baby",
		CanView:     true,
		CanEdit:     true,
		NotifyMode:  models.NotifyModeSilent,
	})
	require.NoError(t, err)

	evSilent := events.MutationEvent{
		Topic:       "baby",
		Action:      "create",
		ActorUserID: "user-sarah",
		FamilyGroup: "TheSmiths",
		Summary:     "Feeding 90ml formula",
	}
	err = bus.Publish(ctx, evSilent)
	require.NoError(t, err)
	assert.Empty(t, notifiedUsers)
}

func TestNotifier_TargetedPartner(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("TheSmiths", []models.User{
		{ID: "user-sarah", PlatformID: "tg-100", Name: "Sarah", FamilyGroup: "TheSmiths"},
		{ID: "user-john", PlatformID: "tg-200", Name: "John", FamilyGroup: "TheSmiths"},
		{ID: "user-bob", PlatformID: "tg-300", Name: "Bob", FamilyGroup: "TheSmiths"},
	})

	var notifiedUsers []string
	mockSender := func(ctx context.Context, recipientID, text string) error {
		notifiedUsers = append(notifiedUsers, recipientID)
		return nil
	}

	bus := events.NewEventBus()
	notifier := NewNotifier(repo, mockSender)
	notifier.RegisterWithBus(bus)

	// Configure house topic targeted specifically to user-john
	err := repo.UpsertSharedSpace(&models.SharedSpace{
		FamilyGroup:      "TheSmiths",
		Topic:            "house",
		SharedWithUserID: "user-john",
		CanView:          true,
		CanEdit:          true,
		NotifyMode:       models.NotifyModeInstant,
	})
	require.NoError(t, err)

	ctx := context.Background()
	ev := events.MutationEvent{
		Topic:       "house",
		Action:      "create",
		ActorUserID: "user-sarah",
		FamilyGroup: "TheSmiths",
		Summary:     "Fixed kitchen sink",
	}

	err = bus.Publish(ctx, ev)
	require.NoError(t, err)

	// Only John should be notified; Bob is excluded
	require.Len(t, notifiedUsers, 1)
	assert.Equal(t, "tg-200", notifiedUsers[0])
}

func TestToolModule_DeclarationsAndPrompt(t *testing.T) {
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	notifier := NewNotifier(repo, nil)
	mod := NewModule(repo, bus, notifier)

	assert.Equal(t, "shared", mod.Name())

	decls := mod.Declarations()
	require.Len(t, decls, 5)

	toolNames := make(map[string]bool)
	for _, d := range decls {
		toolNames[d.Name] = true
	}
	assert.True(t, toolNames["configure_shared_topic"])
	assert.True(t, toolNames["create_shared_reminder"])
	assert.True(t, toolNames["complete_shared_reminder"])
	assert.True(t, toolNames["list_shared_activity"])
	assert.True(t, toolNames["schedule_reminder"])

	ctx := context.Background()

	// User without family group
	promptSolo := mod.SystemPromptSection(ctx, orchestrator.CallContext{
		User: &models.User{ID: "user-1", FamilyGroup: ""},
	})
	assert.Empty(t, promptSolo)

	// User with family group
	promptFamily := mod.SystemPromptSection(ctx, orchestrator.CallContext{
		User: &models.User{ID: "user-1", FamilyGroup: "TheSmiths"},
	})
	assert.Contains(t, promptFamily, "Family Group: TheSmiths")
	assert.Contains(t, promptFamily, "baby, house, finance")
}

func TestToolModule_ExecuteTools(t *testing.T) {
	repo := newInMemoryRepo()
	repo.setUsers("TheSmiths", []models.User{
		{ID: "user-sarah", PlatformID: "tg-100", Name: "Sarah", FamilyGroup: "TheSmiths"},
		{ID: "user-john", PlatformID: "tg-200", Name: "John", FamilyGroup: "TheSmiths"},
	})

	var notifiedUsers []string
	var notifiedTexts []string
	var mu sync.Mutex
	mockSender := func(ctx context.Context, recipientID, text string) error {
		mu.Lock()
		defer mu.Unlock()
		notifiedUsers = append(notifiedUsers, recipientID)
		notifiedTexts = append(notifiedTexts, text)
		return nil
	}

	bus := events.NewEventBus()
	notifier := NewNotifier(repo, mockSender)
	mod := NewModule(repo, bus, notifier)

	ctx := context.Background()
	callCtxSarah := orchestrator.CallContext{
		User:             &models.User{ID: "user-sarah", PlatformID: "tg-100", Name: "Sarah", FamilyGroup: "TheSmiths"},
		EventBus:         bus,
		SendNotification: mockSender,
	}
	callCtxJohn := orchestrator.CallContext{
		User:             &models.User{ID: "user-john", PlatformID: "tg-200", Name: "John", FamilyGroup: "TheSmiths"},
		EventBus:         bus,
		SendNotification: mockSender,
	}

	// 1. Tool: configure_shared_topic
	resConfig, err := mod.Execute(ctx, callCtxSarah, "configure_shared_topic", map[string]interface{}{
		"topic":       "baby",
		"can_view":    true,
		"can_edit":    true,
		"notify_mode": "instant",
	})
	require.NoError(t, err)
	assert.Contains(t, resConfig, "Successfully configured shared topic 'baby'")

	// Check repo
	sp, err := repo.GetSharedSpace("TheSmiths", "baby")
	require.NoError(t, err)
	require.NotNil(t, sp)
	assert.True(t, sp.CanView)

	// 2. Tool: create_shared_reminder
	resCreate, err := mod.Execute(ctx, callCtxSarah, "create_shared_reminder", map[string]interface{}{
		"topic":    "baby",
		"title":    "Give 5 drops Vitamin D",
		"due_time": "2026-11-03T19:00:00Z",
	})
	require.NoError(t, err)
	assert.Contains(t, resCreate, "Created shared task/reminder #1")

	// Verify John got notified of the created reminder
	mu.Lock()
	assert.Len(t, notifiedUsers, 2) // 1 from configure, 1 from create reminder
	assert.Equal(t, "tg-200", notifiedUsers[1])
	assert.Contains(t, notifiedTexts[1], "Give 5 drops Vitamin D")
	mu.Unlock()

	// 3. Tool: list_shared_activity
	resList, err := mod.Execute(ctx, callCtxJohn, "list_shared_activity", map[string]interface{}{
		"topic": "baby",
	})
	require.NoError(t, err)
	assert.Contains(t, resList, "Give 5 drops Vitamin D")
	assert.Contains(t, resList, "⏳ Pending")

	// 4. Tool: complete_shared_reminder (John completes the task)
	resComplete, err := mod.Execute(ctx, callCtxJohn, "complete_shared_reminder", map[string]interface{}{
		"id": float64(1), // test JSON number float64
	})
	require.NoError(t, err)
	assert.Contains(t, resComplete, "marked as completed by John")

	// Verify Sarah got notified of the completion
	mu.Lock()
	assert.Len(t, notifiedUsers, 3)
	assert.Equal(t, "tg-100", notifiedUsers[2]) // Sarah notified!
	assert.Contains(t, notifiedTexts[2], "John completed: Give 5 drops Vitamin D")
	mu.Unlock()

	// List again, now should show completed
	resListAfter, err := mod.Execute(ctx, callCtxSarah, "list_shared_activity", map[string]interface{}{
		"topic":             "baby",
		"include_completed": true,
	})
	require.NoError(t, err)
	assert.Contains(t, resListAfter, "✅ Completed by tg-200")
}

func TestToolModule_ErrorCases(t *testing.T) {
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus, nil)
	ctx := context.Background()

	// 1. Standalone user without family group
	soloCtx := orchestrator.CallContext{
		User: &models.User{ID: "solo-user", FamilyGroup: ""},
	}
	resSolo, err := mod.Execute(ctx, soloCtx, "create_shared_reminder", map[string]interface{}{
		"title": "Buy milk",
	})
	require.NoError(t, err)
	assert.Contains(t, resSolo, "require an active family group")

	// 2. Family user with missing parameters
	famCtx := orchestrator.CallContext{
		User: &models.User{ID: "user-1", FamilyGroup: "FamilyA"},
	}

	// Missing title in create_shared_reminder
	_, err = mod.Execute(ctx, famCtx, "create_shared_reminder", map[string]interface{}{
		"title": "",
	})
	assert.ErrorContains(t, err, "title is required")

	// Missing topic in configure_shared_topic
	_, err = mod.Execute(ctx, famCtx, "configure_shared_topic", map[string]interface{}{
		"topic": "",
	})
	assert.ErrorContains(t, err, "topic is required")

	// Missing id in complete_shared_reminder
	_, err = mod.Execute(ctx, famCtx, "complete_shared_reminder", map[string]interface{}{})
	assert.ErrorContains(t, err, "id is required")

	// Unknown tool
	_, err = mod.Execute(ctx, famCtx, "non_existent_tool", map[string]interface{}{})
	assert.ErrorContains(t, err, "unknown shared tool")
}

// TestSEC04_CrossTenant_SharedReminders_Blocked empirically verifies that shared reminders
// cannot be completed across families, and verifies legitimate same-family completions.
func TestSEC04_CrossTenant_SharedReminders_Blocked(t *testing.T) {
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus, nil)
	ctx := context.Background()

	// 1. Family Alpha creates a shared reminder
	callCtxAlpha := orchestrator.CallContext{
		User:    &models.User{ID: "u_alpha", PlatformID: "user_alpha", FamilyGroup: "family_alpha", Name: "Alpha User"},
		Message: models.Message{UserID: "user_alpha"},
	}

	createResp, err := mod.Execute(ctx, callCtxAlpha, "create_shared_reminder", map[string]interface{}{
		"title": "Alpha Grocery Shopping",
		"topic": "groceries",
	})
	require.NoError(t, err)
	assert.Contains(t, createResp, "Alpha Grocery Shopping")

	// Verify reminder 1 exists and is not completed
	listAlpha, err := repo.ListSharedReminders("family_alpha", "groceries", true)
	require.NoError(t, err)
	require.Len(t, listAlpha, 1)
	assert.Equal(t, uint(1), listAlpha[0].ID)
	assert.False(t, listAlpha[0].IsCompleted)

	// 2. Family Beta attempts to complete Family Alpha's reminder (id: 1)
	callCtxBeta := orchestrator.CallContext{
		User:    &models.User{ID: "u_beta", PlatformID: "user_beta", FamilyGroup: "family_beta", Name: "Beta Attacker"},
		Message: models.Message{UserID: "user_beta"},
	}

	_, completeBetaErr := mod.Execute(ctx, callCtxBeta, "complete_shared_reminder", map[string]interface{}{
		"id": 1,
	})
	assert.Error(t, completeBetaErr, "Family Beta must NOT be able to complete Family Alpha's reminder")

	// Verify reminder 1 is STILL not completed
	listAlphaAfter, err := repo.ListSharedReminders("family_alpha", "groceries", true)
	require.NoError(t, err)
	require.Len(t, listAlphaAfter, 1)
	assert.False(t, listAlphaAfter[0].IsCompleted)

	// 3. Legitimate same-family completion (non-regression)
	completeAlphaResp, err := mod.Execute(ctx, callCtxAlpha, "complete_shared_reminder", map[string]interface{}{
		"id": 1,
	})
	require.NoError(t, err, "Family Alpha must be able to complete their own reminder")
	assert.Contains(t, completeAlphaResp, "completed")

	// Verify reminder 1 is now completed
	listAlphaFinal, err := repo.ListSharedReminders("family_alpha", "groceries", true)
	require.NoError(t, err)
	require.Len(t, listAlphaFinal, 1)
	assert.True(t, listAlphaFinal[0].IsCompleted)
	assert.Equal(t, "user_alpha", listAlphaFinal[0].CompletedByUserID)
}

type testSharedDBClient struct {
	orchestrator.DBClient
	savedReminders []models.ScheduledReminder
}

func (m *testSharedDBClient) SaveReminder(r models.ScheduledReminder) error {
	m.savedReminders = append(m.savedReminders, r)
	return nil
}

func TestScheduleReminder_Execution(t *testing.T) {
	ctx := context.Background()
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	mod := NewModule(repo, bus, nil)

	dbMock := &testSharedDBClient{}

	callCtx := orchestrator.CallContext{
		User: &models.User{
			PlatformID:  "tg_456",
			FamilyGroup: "FamTest",
		},
		DB:       dbMock,
		EventBus: bus,
	}

	res, err := mod.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
		"message":  "Pick up dry cleaning",
		"due_time": "2026-11-10T15:00:00Z",
	})
	require.NoError(t, err)
	assert.Contains(t, res, "Reminder Set!")
	assert.Contains(t, res, "Pick up dry cleaning")

	require.Len(t, dbMock.savedReminders, 1)
	assert.Equal(t, "tg_456", dbMock.savedReminders[0].UserID)
	assert.Equal(t, "Pick up dry cleaning", dbMock.savedReminders[0].Message)
	assert.False(t, dbMock.savedReminders[0].IsSent)
}


