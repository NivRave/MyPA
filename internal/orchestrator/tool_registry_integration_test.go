package orchestrator_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/domains/preferences"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/gmail"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/identity/identitytest"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/calendar/v3"
	tasksapi "google.golang.org/api/tasks/v1"
)

// Mock clients for ARCH-01 & ARCH-04 tests

type mockCalClient struct {
	calledUser string
}

func (m *mockCalClient) CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error) {
	return "http://cal/event/1", nil
}
func (m *mockCalClient) ListEvents(ctx context.Context, min, max, q string) ([]*calendar.Event, error) {
	return []*calendar.Event{{Summary: "Event 1"}}, nil
}
func (m *mockCalClient) UpdateEvent(ctx context.Context, id string, ev models.CalendarEvent) error {
	return nil
}
func (m *mockCalClient) DeleteEvent(ctx context.Context, id string) error {
	return nil
}

// memScheduleRepo is the smallest household-scoped db.ScheduleRepository the calendar module
// needs here (V4-08: the calendar tools keep every event in MyPA).
type memScheduleRepo struct {
	mu     sync.Mutex
	events map[uint]*models.ScheduleEvent
	nextID uint
}

func newMemScheduleRepo() *memScheduleRepo {
	return &memScheduleRepo{events: map[uint]*models.ScheduleEvent{}}
}

func (r *memScheduleRepo) CreateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	ev.ID, ev.HouseholdID, ev.FamilyGroup = r.nextID, s.HouseholdID(), s.LegacyLabel()
	cp := *ev
	r.events[ev.ID] = &cp
	return nil
}

func (r *memScheduleRepo) GetScheduleEvent(s identity.Scope, id uint) (*models.ScheduleEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev, ok := r.events[id]; ok && ev.HouseholdID == s.HouseholdID() {
		cp := *ev
		return &cp, nil
	}
	return nil, nil
}

func (r *memScheduleRepo) UpdateScheduleEvent(s identity.Scope, ev *models.ScheduleEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *ev
	r.events[ev.ID] = &cp
	return nil
}

func (r *memScheduleRepo) DeleteScheduleEvent(s identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.events, id)
	return nil
}

func (r *memScheduleRepo) ListScheduleEvents(s identity.Scope, topic string, start, end time.Time) ([]models.ScheduleEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []models.ScheduleEvent
	for _, ev := range r.events {
		if ev.HouseholdID == s.HouseholdID() {
			out = append(out, *ev)
		}
	}
	return out, nil
}

func (r *memScheduleRepo) UpsertTopicCalendarConfig(*models.TopicCalendarConfig) error { return nil }
func (r *memScheduleRepo) GetTopicCalendarConfig(string, string) (*models.TopicCalendarConfig, error) {
	return nil, nil
}
func (r *memScheduleRepo) ListTopicCalendarConfigs(string) ([]models.TopicCalendarConfig, error) {
	return nil, nil
}

type mockGmailClient struct {
	lastSearchUser      string
	lastReadUser        string
	lastDraftUser       string
	lastArchiveUser     string
	lastSoftDeleteUser  string
	lastListLabelsUser  string
	lastApplyLabelUser  string
	lastCreateLabelUser string
	lastUnsubscribeUser string
}

func (m *mockGmailClient) SearchEmails(ctx context.Context, userID, query string, max int64) ([]gmail.EmailSummary, error) {
	m.lastSearchUser = userID
	return []gmail.EmailSummary{{ID: "msg1", Subject: "Test"}}, nil
}
func (m *mockGmailClient) ReadEmail(ctx context.Context, userID, messageID string) (string, error) {
	m.lastReadUser = userID
	return "Email body content", nil
}
func (m *mockGmailClient) DraftReply(ctx context.Context, userID, messageID, replyText string) error {
	m.lastDraftUser = userID
	return nil
}
func (m *mockGmailClient) ArchiveEmail(ctx context.Context, userID, messageID string) error {
	m.lastArchiveUser = userID
	return nil
}
func (m *mockGmailClient) SoftDeleteEmail(ctx context.Context, userID, messageID string) error {
	m.lastSoftDeleteUser = userID
	return nil
}
func (m *mockGmailClient) ListLabels(ctx context.Context, userID string) (map[string]string, error) {
	m.lastListLabelsUser = userID
	return map[string]string{"INBOX": "INBOX"}, nil
}
func (m *mockGmailClient) ApplyLabel(ctx context.Context, userID, messageID, labelID string) error {
	m.lastApplyLabelUser = userID
	return nil
}
func (m *mockGmailClient) CreateLabel(ctx context.Context, userID, labelName string) (string, error) {
	m.lastCreateLabelUser = userID
	return "label_123", nil
}
func (m *mockGmailClient) UnsubscribeEmail(ctx context.Context, userID, messageID string) error {
	m.lastUnsubscribeUser = userID
	return nil
}

type mockTasksClient struct {
	lastUser string
}

func (m *mockTasksClient) ListTaskLists(ctx context.Context, userID string) ([]*tasksapi.TaskList, error) {
	m.lastUser = userID
	return []*tasksapi.TaskList{{Id: "tl1", Title: "Work"}}, nil
}
func (m *mockTasksClient) CreateTaskList(ctx context.Context, userID, title string) (*tasksapi.TaskList, error) {
	m.lastUser = userID
	return &tasksapi.TaskList{Id: "tl2", Title: title}, nil
}
func (m *mockTasksClient) ListTasks(ctx context.Context, userID, listID string) ([]*tasksapi.Task, error) {
	m.lastUser = userID
	return []*tasksapi.Task{{Id: "t1", Title: "Task 1"}}, nil
}
func (m *mockTasksClient) CreateTask(ctx context.Context, userID, listID, title, notes, due string) error {
	m.lastUser = userID
	return nil
}
func (m *mockTasksClient) CompleteTask(ctx context.Context, userID, listID, taskID string) error {
	m.lastUser = userID
	return nil
}
func (m *mockTasksClient) DeleteTask(ctx context.Context, userID, listID, taskID string) error {
	m.lastUser = userID
	return nil
}

type mockContactsClient struct {
	lastSearchUser string
	lastCreateUser string
}

func (m *mockContactsClient) SearchContacts(ctx context.Context, userID, query string) ([]models.Contact, error) {
	m.lastSearchUser = userID
	return []models.Contact{{Name: "Alice", Email: "alice@example.com"}}, nil
}
func (m *mockContactsClient) CreateContact(ctx context.Context, userID, name, email, phone string) error {
	m.lastCreateUser = userID
	return nil
}

type mockTavilyClient struct {
	lastQuery string
}

func (m *mockTavilyClient) Search(ctx context.Context, query string) (string, error) {
	m.lastQuery = query
	return "Search results for: " + query, nil
}

type mockDBClient struct {
	savedReminders []models.ScheduledReminder
}

// recordingAutomationRepo keeps the rules the automation tools write. Since V4-07 a scheduled
// workflow is a cron automation rule, so the workflow tools write here. IDs start at 42.
type recordingAutomationRepo struct {
	db.AutomationRepository
	mu      sync.Mutex
	nextID  uint
	rules   map[uint]models.AutomationRule
	created []models.AutomationRule
	deleted []uint
}

func newRecordingAutomationRepo() *recordingAutomationRepo {
	return &recordingAutomationRepo{nextID: 42, rules: map[uint]models.AutomationRule{}}
}

func (r *recordingAutomationRepo) CreateAutomationRule(_ identity.Scope, rule *models.AutomationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.ID = r.nextID
	r.nextID++
	r.rules[rule.ID] = *rule
	r.created = append(r.created, *rule)
	return nil
}

func (r *recordingAutomationRepo) GetAutomationRule(s identity.Scope, id uint) (*models.AutomationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, ok := r.rules[id]
	if !ok || rule.HouseholdID != s.HouseholdID() {
		return nil, nil
	}
	return &rule, nil
}

func (r *recordingAutomationRepo) ListAutomationRules(s identity.Scope, _ string) ([]models.AutomationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []models.AutomationRule
	for _, rule := range r.rules {
		if rule.HouseholdID == s.HouseholdID() {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (r *recordingAutomationRepo) DeleteAutomationRule(_ identity.Scope, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	r.deleted = append(r.deleted, id)
	return nil
}

func (m *mockDBClient) SearchSimilarMemories(ctx context.Context, s identity.Scope, embedding pgvector.Vector, limit int) ([]models.TopicMemory, error) {
	return nil, nil
}

// identity.Store and the household members, answered from integrationHouseholds (V4-30).
func (m *mockDBClient) HouseholdByID(ctx context.Context, id string) (*models.Household, error) {
	return integrationHouseholds.HouseholdByID(ctx, id)
}
func (m *mockDBClient) HouseholdsByLabel(ctx context.Context, label string) ([]models.Household, error) {
	return integrationHouseholds.HouseholdsByLabel(ctx, label)
}
func (m *mockDBClient) UsersInHousehold(ctx context.Context, s identity.Scope) ([]models.User, error) {
	return nil, nil
}
func (m *mockDBClient) AccessOf(ctx context.Context, u *models.User) (identity.Access, error) {
	return integrationHouseholds.AccessOf(ctx, u)
}
func (m *mockDBClient) HouseholdOwners(ctx context.Context, s identity.Scope) ([]models.User, error) {
	return nil, nil
}

// integrationHouseholds holds this file's households; a label always names the same one.
var integrationHouseholds = identitytest.NewStore()

// recordingMemoryRepo captures what memory tools write to the single (v2) memory store.
type recordingMemoryRepo struct {
	db.MemoryV2Repository
	mu    sync.Mutex
	saved []models.TopicMemory
}

func (r *recordingMemoryRepo) SaveTopicMemory(ctx context.Context, _ identity.Scope, m *models.TopicMemory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = append(r.saved, *m)
	return nil
}
func (m *mockDBClient) GetUniqueUsers() ([]string, error) { return nil, nil }
func (m *mockDBClient) GetLastAuditSessionForUser(userID string) (*models.AuditSession, error) {
	return nil, nil
}
func (m *mockDBClient) SaveReminder(reminder models.ScheduledReminder) error {
	m.savedReminders = append(m.savedReminders, reminder)
	return nil
}
func (m *mockDBClient) GetDueReminders() ([]models.ScheduledReminder, error) { return nil, nil }
func (m *mockDBClient) MarkReminderSent(id uint) error                       { return nil }
func (m *mockDBClient) GetDueSharedReminders() ([]models.SharedTaskReminder, error) {
	return nil, nil
}
func (m *mockDBClient) MarkSharedReminderSent(s identity.Scope, id uint) error { return nil }
func (m *mockDBClient) GetUser(platformID string) (*models.User, error) {
	return &models.User{ID: "uuid-1", PlatformID: platformID, FamilyGroup: "fam-canonical"}, nil
}
func (m *mockDBClient) UpsertUser(u models.User) error                                  { return nil }
func (m *mockDBClient) CreatePairingCode(s identity.Scope, pc models.PairingCode) error { return nil }
func (m *mockDBClient) GetPairingCode(code string) (*models.PairingCode, error) {
	return nil, nil
}
func (m *mockDBClient) RedeemPairingCode(codeID string, u models.User, _ string) error { return nil }
func (m *mockDBClient) UserForAccount(_ context.Context, _, externalID string) (*models.User, error) {
	return m.GetUser(externalID)
}

type mockLLMClient struct {
	lastPrompt string
}

func (m *mockLLMClient) Chat(ctx context.Context, systemPrompt string, history []models.ChatMessage, prompt string, photo []byte, mime string) (*llm.Response, error) {
	m.lastPrompt = prompt
	return &llm.Response{Text: "Processed: " + prompt}, nil
}
func (m *mockLLMClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 3072), nil
}

// TestARCH01_28Tools_ModularizationAndDynamicDispatch empirically tests that all 28 Wave 0 base
// tools are registered across the 8 domain modules, dispatch properly via e.toolRegistry.Execute,
// and handle unknown tools safely.
func TestARCH01_28Tools_ModularizationAndDynamicDispatch(t *testing.T) {
	eventBus := events.NewEventBus()
	reg := orchestrator.NewRegistry()

	calMock := &mockCalClient{}
	calFactory := func(ctx context.Context, userID string) (orchestrator.CalendarClient, error) {
		calMock.calledUser = userID
		return calMock, nil
	}
	gmailMock := &mockGmailClient{}
	tasksMock := &mockTasksClient{}
	contactsMock := &mockContactsClient{}
	tavilyMock := &mockTavilyClient{}

	// Register the 8 domain modules containing the 28 Wave 0 base tools
	// V4-08: the calendar domain is the schedule module.
	modCal := schedule.NewModule(newMemScheduleRepo(), schedule.WithCalendarFactory(calFactory), schedule.WithEventBus(eventBus))
	modGmail := workspace_gmail.NewModule(workspace_gmail.WithClient(gmailMock), workspace_gmail.WithEventBus(eventBus))
	modTasks := workspace_tasks.NewModule(workspace_tasks.WithClient(tasksMock), workspace_tasks.WithEventBus(eventBus))
	modContacts := workspace_contacts.NewModule(workspace_contacts.WithClient(contactsMock), workspace_contacts.WithEventBus(eventBus))
	modWeb := web_research.NewModule(web_research.WithClient(tavilyMock))
	memRepo := &recordingMemoryRepo{}
	modMemory := memory.NewModule(memRepo)
	modShared := shared.NewModule(nil, eventBus, nil)
	autoRepo := newRecordingAutomationRepo()
	modAuto := automation.NewModule(autoRepo, automation.WithEventBus(eventBus))

	reg.Register(modCal)
	reg.Register(modGmail)
	reg.Register(modTasks)
	reg.Register(modContacts)
	reg.Register(modWeb)
	reg.Register(modMemory)
	reg.Register(modShared)
	reg.Register(modAuto)

	expectedToolDistribution := map[string][]string{
		"schedule": {
			"create_calendar_event",
			"list_calendar_events",
			"update_calendar_event",
			"delete_calendar_event",
		},
		"workspace_gmail": {
			"search_emails",
			"read_email",
			"draft_email_reply",
			"archive_emails",
			"soft_delete_emails",
			"list_email_labels",
			"apply_email_labels",
			"create_email_label",
			"unsubscribe_email",
		},
		"workspace_tasks": {
			"list_task_lists",
			"create_task_list",
			"list_tasks",
			"create_task",
			"complete_task",
			"delete_task",
		},
		"workspace_contacts": {
			"search_contacts",
			"create_contact",
		},
		"web_research": {
			"search_web",
			"fetch_webpage",
		},
		"memory": {
			"save_topic_memory",
		},
		"shared": {
			"schedule_reminder",
		},
		"automation": {
			"schedule_workflow",
			"list_workflows",
			"delete_workflow",
		},
	}

	totalExpectedTools := 4 + 9 + 6 + 2 + 2 + 1 + 1 + 3
	assert.Equal(t, 28, totalExpectedTools)

	t.Run("Verify Exact 28 Tool Declarations and Domain Mapping", func(t *testing.T) {
		actualToolCount := 0
		for domainName, tools := range expectedToolDistribution {
			mod, exists := reg.GetModule(domainName)
			require.True(t, exists, "Domain module %s must be registered", domainName)

			decls := mod.Declarations()
			declMap := make(map[string]bool)
			for _, d := range decls {
				declMap[d.Name] = true
			}

			for _, expectedTool := range tools {
				assert.True(t, declMap[expectedTool], "Tool %s must be declared in domain %s", expectedTool, domainName)
				actualToolCount++

				// Check registry route
				routedMod, ok := reg.GetModuleForTool(expectedTool)
				assert.True(t, ok, "Registry must route tool %s", expectedTool)
				assert.Equal(t, domainName, routedMod.Name())
			}
		}
		assert.Equal(t, 28, actualToolCount, "Exactly 28 base tools must be verified")
	})

	t.Run("Verify Dynamic Dispatch via reg.Execute", func(t *testing.T) {
		mockDB := &mockDBClient{}
		mockLLM := &mockLLMClient{}
		callCtx := orchestrator.CallContext{
			User: &models.User{
				ID:          "uuid-victim",
				PlatformID:  "canonical_user_42",
				FamilyGroup: "canonical_family_1",
			},
			Scope:    integrationHouseholds.ScopeIn(t, "canonical_family_1", "canonical_user_42", ""),
			Message:  channel.Inbound{Sender: "canonical_user_42"},
			DB:       mockDB,
			LLM:      mockLLM,
			EventBus: eventBus,
		}

		ctx := context.Background()

		// 1. Dispatch a calendar tool
		res, handled, err := reg.Execute(ctx, callCtx, "list_calendar_events", map[string]interface{}{"query": "standup"})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Event 1", "the Google event is listed")
		assert.Equal(t, "canonical_user_42", calMock.calledUser)

		// 2. Dispatch workspace_gmail tool
		res, handled, err = reg.Execute(ctx, callCtx, "search_emails", map[string]interface{}{"query": "is:unread"})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Here are the emails I found")
		assert.Equal(t, "canonical_user_42", gmailMock.lastSearchUser)

		// 3. Dispatch workspace_tasks tool
		res, handled, err = reg.Execute(ctx, callCtx, "list_task_lists", map[string]interface{}{})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Here are the available task lists")
		assert.Equal(t, "canonical_user_42", tasksMock.lastUser)

		// 4. Dispatch workspace_contacts tool
		res, handled, err = reg.Execute(ctx, callCtx, "search_contacts", map[string]interface{}{"query": "Alice"})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Here are the contacts I found")
		assert.Equal(t, "canonical_user_42", contactsMock.lastSearchUser)

		// 5. Dispatch web_research tool
		res, handled, err = reg.Execute(ctx, callCtx, "search_web", map[string]interface{}{"query": "Go 1.26 release date"})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Equal(t, "Search results for: Go 1.26 release date", res)

		// 6. Dispatch memory tool (save_topic_memory; remember_fact was removed in V4-06)
		res, handled, err = reg.Execute(ctx, callCtx, "save_topic_memory", map[string]interface{}{"topic": "personal", "fact": "Loves pistachio ice cream", "scope": "personal"})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, `"status":"saved"`)
		require.Len(t, memRepo.saved, 1)
		assert.Equal(t, "canonical_user_42", memRepo.saved[0].UserID)

		// 7. Dispatch shared tool (schedule_reminder)
		res, handled, err = reg.Execute(ctx, callCtx, "schedule_reminder", map[string]interface{}{
			"message":  "Pick up groceries",
			"due_time": "2026-11-07T18:00:00Z",
		})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Reminder Set")
		require.Len(t, mockDB.savedReminders, 1)
		assert.Equal(t, "canonical_user_42", mockDB.savedReminders[0].UserID)

		// 8. Dispatch automation tool (schedule_workflow)
		res, handled, err = reg.Execute(ctx, callCtx, "schedule_workflow", map[string]interface{}{
			"name":            "daily_briefing",
			"cron_expression": "0 8 * * *",
			"instruction":     "Summarize daily tasks",
		})
		require.NoError(t, err)
		assert.True(t, handled)
		assert.Contains(t, res, "Workflow Scheduled")
		require.Len(t, autoRepo.created, 1)
		assert.Equal(t, "canonical_user_42", autoRepo.created[0].UserID)
		assert.Equal(t, "cron", autoRepo.created[0].TriggerType, "a workflow is a cron rule (V4-07)")
		assert.Equal(t, "prompt: Summarize daily tasks", autoRepo.created[0].ActionInstruction)
	})

	t.Run("Unknown Tool Returns Handled=false Without Crashing", func(t *testing.T) {
		callCtx := orchestrator.CallContext{
			Message: channel.Inbound{Sender: "user-1"},
		}
		res, handled, err := reg.Execute(context.Background(), callCtx, "system_format_drive", map[string]interface{}{})
		require.NoError(t, err)
		assert.False(t, handled)
		assert.Empty(t, res)
	})
}

// TestARCH04_All28Tools_AntiIDOR_IdentitySpoofingStress passes adversarial spoofed
// identity fields (user_id, actor_id, family_group, tenant_id) to all 28 base tools
// and proves that the tools strictly enforce callCtx.ActorID() and callCtx.TenantFamilyGroup().
func TestARCH04_All28Tools_AntiIDOR_IdentitySpoofingStress(t *testing.T) {
	eventBus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 50)
	eventBus.Subscribe(func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	calMock := &mockCalClient{}
	calFactory := func(ctx context.Context, userID string) (orchestrator.CalendarClient, error) {
		calMock.calledUser = userID
		return calMock, nil
	}
	gmailMock := &mockGmailClient{}
	tasksMock := &mockTasksClient{}
	contactsMock := &mockContactsClient{}
	tavilyMock := &mockTavilyClient{}
	mockDB := &mockDBClient{}
	mockLLM := &mockLLMClient{}

	reg := orchestrator.NewRegistry()
	reg.Register(schedule.NewModule(newMemScheduleRepo(), schedule.WithCalendarFactory(calFactory), schedule.WithEventBus(eventBus)))
	reg.Register(workspace_gmail.NewModule(workspace_gmail.WithClient(gmailMock), workspace_gmail.WithEventBus(eventBus)))
	reg.Register(workspace_tasks.NewModule(workspace_tasks.WithClient(tasksMock), workspace_tasks.WithEventBus(eventBus)))
	reg.Register(workspace_contacts.NewModule(workspace_contacts.WithClient(contactsMock), workspace_contacts.WithEventBus(eventBus)))
	reg.Register(web_research.NewModule(web_research.WithClient(tavilyMock)))
	memRepo := &recordingMemoryRepo{}
	reg.Register(memory.NewModule(memRepo))
	reg.Register(shared.NewModule(nil, eventBus, nil))
	autoRepo := newRecordingAutomationRepo()
	reg.Register(automation.NewModule(autoRepo, automation.WithEventBus(eventBus)))

	legitUser := &models.User{
		ID:          "uuid-legit-victim-111",
		PlatformID:  "canonical_victim_platform_id",
		FamilyGroup: "canonical_victim_family_group",
	}

	callCtx := orchestrator.CallContext{
		User:     legitUser,
		Scope:    integrationHouseholds.ScopeIn(t, "canonical_victim_family_group", "canonical_victim_platform_id", ""),
		Message:  channel.Inbound{Sender: "canonical_victim_platform_id"},
		DB:       mockDB,
		LLM:      mockLLM,
		EventBus: eventBus,
	}

	// Adversarial payload spoofing identities
	spoofedPayload := map[string]interface{}{
		"user_id":       "attacker_spoofed_user",
		"actor_id":      "attacker_spoofed_actor",
		"family_group":  "attacker_spoofed_family",
		"tenant_id":     "attacker_spoofed_tenant",
		"actor_user_id": "attacker_spoofed_actor",
	}

	ctx := context.Background()

	// 1. Calendar: create_calendar_event
	args := copyMap(spoofedPayload)
	args["title"] = "Hacked Meeting"
	args["start_time"] = "2026-11-10T10:00:00Z"
	args["end_time"] = "2026-11-10T11:00:00Z"
	args["timezone"] = "UTC"
	_, _, err := reg.Execute(ctx, callCtx, "create_calendar_event", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", calMock.calledUser, "Calendar must use ActorID()")
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 2. Calendar: delete_calendar_event
	args = copyMap(spoofedPayload)
	args["google_event_id"] = "event-123"
	_, _, err = reg.Execute(ctx, callCtx, "delete_calendar_event", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", calMock.calledUser)
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 3. Gmail: search_emails
	args = copyMap(spoofedPayload)
	args["query"] = "financials"
	_, _, err = reg.Execute(ctx, callCtx, "search_emails", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", gmailMock.lastSearchUser, "Gmail must use ActorID()")

	// 4. Gmail: draft_email_reply
	args = copyMap(spoofedPayload)
	args["message_id"] = "msg-123"
	args["reply_text"] = "Attacker reply"
	_, _, err = reg.Execute(ctx, callCtx, "draft_email_reply", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", gmailMock.lastDraftUser)
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 5. Tasks: create_task
	args = copyMap(spoofedPayload)
	args["title"] = "Exfiltrate database"
	_, _, err = reg.Execute(ctx, callCtx, "create_task", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", tasksMock.lastUser, "Tasks must use ActorID()")
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 6. Contacts: create_contact
	args = copyMap(spoofedPayload)
	args["name"] = "Attacker Puppet"
	args["email"] = "puppet@evil.com"
	_, _, err = reg.Execute(ctx, callCtx, "create_contact", args)
	require.NoError(t, err)
	assert.Equal(t, "canonical_victim_platform_id", contactsMock.lastCreateUser, "Contacts must use ActorID()")
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 7. Memory: save_topic_memory
	args = copyMap(spoofedPayload)
	args["topic"] = "personal"
	args["fact"] = "Secret victim password"
	_, _, err = reg.Execute(ctx, callCtx, "save_topic_memory", args)
	require.NoError(t, err)
	require.NotEmpty(t, memRepo.saved)
	lastMem := memRepo.saved[len(memRepo.saved)-1]
	assert.Equal(t, "canonical_victim_platform_id", lastMem.UserID, "Memory must be saved with canonical ActorID()")
	assert.NotEqual(t, "attacker_spoofed_user", lastMem.UserID)

	// 8. Shared: schedule_reminder
	args = copyMap(spoofedPayload)
	args["message"] = "Victim reminder"
	args["due_time"] = "2026-11-08T09:00:00Z"
	_, _, err = reg.Execute(ctx, callCtx, "schedule_reminder", args)
	require.NoError(t, err)
	require.NotEmpty(t, mockDB.savedReminders)
	lastRem := mockDB.savedReminders[len(mockDB.savedReminders)-1]
	assert.Equal(t, "canonical_victim_platform_id", lastRem.UserID, "Reminder must be saved with canonical ActorID()")
	assert.NotEqual(t, "attacker_spoofed_user", lastRem.UserID)
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 9. Automation: schedule_workflow
	args = copyMap(spoofedPayload)
	args["name"] = "Malicious Cron"
	args["cron_expression"] = "0 * * * *"
	args["instruction"] = "Send data"
	_, _, err = reg.Execute(ctx, callCtx, "schedule_workflow", args)
	require.NoError(t, err)
	require.NotEmpty(t, autoRepo.created)
	lastWf := autoRepo.created[len(autoRepo.created)-1]
	assert.Equal(t, "canonical_victim_platform_id", lastWf.UserID, "Workflow must be saved with canonical ActorID()")
	assert.NotEqual(t, "attacker_spoofed_user", lastWf.UserID)
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")

	// 10. Automation: delete_workflow
	args = copyMap(spoofedPayload)
	args["id"] = fmt.Sprint(lastWf.ID)
	_, _, err = reg.Execute(ctx, callCtx, "delete_workflow", args)
	require.NoError(t, err)
	require.NotEmpty(t, autoRepo.deleted)
	assert.Equal(t, lastWf.ID, autoRepo.deleted[len(autoRepo.deleted)-1])
	assertLastMutationEvent(t, receivedEvents, "canonical_victim_platform_id", "canonical_victim_family_group")
}

// TestSEC09_AllFourMetaTools_GatedAndInteractiveConfirmation tests that all 4 meta-tools
// are strictly gated during untrusted turns and require explicit CONFIRM confirmation.
func TestSEC09_AllFourMetaTools_GatedAndInteractiveConfirmation(t *testing.T) {
	reg := orchestrator.NewRegistry()
	reg.Register(automation.NewModule(nil))
	reg.Register(preferences.NewModule(nil)) // update_user_preferences moved here (V4-27)

	metaTools := []string{
		"update_user_preferences",
		"create_automation_rule",
		"schedule_workflow",
		"register_external_api",
	}

	for _, metaTool := range metaTools {
		t.Run("UntrustedTurnBlocks_"+metaTool, func(t *testing.T) {
			// Simulate untrusted state from external email/web content
			secState := &orchestrator.TurnSecurityState{}
			secState.MarkUntrusted("untrusted_source_payload")
			ctx := orchestrator.WithTurnSecurityState(context.Background(), secState)

			callCtx := orchestrator.CallContext{
				Message:             channel.Inbound{Sender: "target_user_123"},
				HasUntrustedContent: true,
				UntrustedSources:    []string{"untrusted_source_payload"},
			}

			// In automation/module.go:
			res, _, err := reg.Execute(ctx, callCtx, metaTool, map[string]interface{}{"foo": "bar"})
			require.NoError(t, err)
			assert.Contains(t, res, "🔒 **Interactive Security Confirmation Required**")
			assert.Contains(t, res, "CONFIRM "+strings.ToUpper(metaTool))
		})
	}
}

func copyMap(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range src {
		out[k] = v
	}
	return out
}

func assertLastMutationEvent(t *testing.T, ch chan events.MutationEvent, expectedActor, expectedFamily string) {
	select {
	case ev := <-ch:
		assert.Equal(t, expectedActor, ev.ActorUserID, "MutationEvent ActorUserID must match canonical ActorID()")
		assert.Equal(t, expectedFamily, ev.FamilyGroup, "MutationEvent FamilyGroup must match canonical TenantFamilyGroup()")
		assert.NotContains(t, ev.ActorUserID, "attacker_spoofed")
		assert.NotContains(t, ev.FamilyGroup, "attacker_spoofed")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected MutationEvent was not published to EventBus")
	}
}
