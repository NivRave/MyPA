package shared

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Repository defines the database operations needed by the Shared ToolModule. Every method is
// confined to the caller's household (V4-30); UsersInHousehold resolves the people a tool
// argument names (identity.Member).
type Repository interface {
	UpsertSharedSpace(s identity.Scope, space *models.SharedSpace) error
	GetSharedSpacesForFamily(s identity.Scope) ([]models.SharedSpace, error)
	GetSharedSpace(s identity.Scope, topic string) (*models.SharedSpace, error)
	CreateSharedReminder(s identity.Scope, reminder *models.SharedTaskReminder) error
	CompleteSharedReminder(s identity.Scope, id uint, completedByUserID string) (*models.SharedTaskReminder, error)
	ListSharedReminders(s identity.Scope, topic string, includeCompleted bool) ([]models.SharedTaskReminder, error)
	UsersInHousehold(ctx context.Context, s identity.Scope) ([]models.User, error)
}

// Module implements the orchestrator.ToolModule interface for multi-user shared spaces,
// collaborative task hand-offs, and cross-user notifications.
type Module struct {
	repo     Repository
	eventBus *events.EventBus
	notifier *Notifier
}

// NewModule constructs a new Shared ToolModule.
func NewModule(repo Repository, eventBus *events.EventBus, notifier *Notifier) *Module {
	mod := &Module{
		repo:     repo,
		eventBus: eventBus,
		notifier: notifier,
	}
	if eventBus != nil && notifier != nil {
		notifier.RegisterWithBus(eventBus)
	}
	return mod
}

// Name returns the unique module identifier.
func (m *Module) Name() string {
	return "shared"
}

// Declarations provides Gemini FunctionDeclaration schemas for shared space tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "configure_shared_topic",
			Description: "Configure family sharing rules, access permissions, and notification modes for a topic: one of the household's topics (e.g., 'family', 'work', 'personal') or 'schedule'.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "The topic to configure: one of the household's topics (e.g., 'family', 'personal', 'work') or 'schedule'.",
					},
					"can_view": {
						Type:        genai.TypeBoolean,
						Description: "Whether family members can view records in this topic (default true).",
					},
					"can_edit": {
						Type:        genai.TypeBoolean,
						Description: "Whether family members can create or edit records in this topic (default true).",
					},
					"notify_mode": {
						Type:        genai.TypeString,
						Description: "Notification mode for changes in this topic: 'instant' (push alerts to partner), 'digest' (batched summaries), or 'silent' (no proactive push). Default 'instant'.",
					},
					"shared_with_user_id": {
						Type:        genai.TypeString,
						Description: "Optional specific family member user ID to share with, or empty/omit to share with all members of the family group.",
					},
				},
				Required: []string{"topic"},
			},
		},
		{
			Name:        "create_shared_reminder",
			Description: "Create a shared family task or reminder under a topic (one of the household's topics, e.g. 'family', 'health') that can be claimed and completed by any family member.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "The title or action of the shared reminder/task (e.g., 'Give Vitamin D drops at 19:00', 'Buy milk', 'Pay electricity bill').",
					},
					"topic": {
						Type:        genai.TypeString,
						Description: "The shared topic: one of the household's topics (e.g., 'family', 'health'). Default is 'family'.",
					},
					"due_time": {
						Type:        genai.TypeString,
						Description: dueTimeDescription,
					},
					"assigned_to_user_id": {
						Type:        genai.TypeString,
						Description: "Optional family member user ID to assign the task to.",
					},
					"urgent": {
						Type:        genai.TypeBoolean,
						Description: urgentDescription,
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "complete_shared_reminder",
			Description: "Claim and complete a shared family task or reminder, recording who completed it, updating status, and notifying family members.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeInteger,
						Description: "The ID of the shared reminder or task to mark as completed.",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "list_shared_activity",
			Description: "Query configured shared spaces and active or completed shared family tasks and reminders.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Optional topic filter: one of the household's topics (e.g. 'family'). Omit or pass '*' for all topics.",
					},
					"include_completed": {
						Type:        genai.TypeBoolean,
						Description: "Whether to include completed tasks and reminders (default false).",
					},
				},
			},
		},
		{
			Name:        "schedule_reminder",
			Description: "Schedules a point-in-time reminder alert for the user at a specified due date and time.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message": {
						Type:        genai.TypeString,
						Description: "The reminder message or task to be reminded about.",
					},
					"due_time": {
						Type:        genai.TypeString,
						Description: dueTimeDescription,
					},
					"urgent": {
						Type:        genai.TypeBoolean,
						Description: urgentDescription,
					},
				},
				Required: []string{"message", "due_time"},
			},
		},
	}
}

// Execute dispatches and runs the requested tool.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	if toolName == "schedule_reminder" {
		return m.executeScheduleReminder(ctx, callCtx, args)
	}

	repo := m.resolveRepo(callCtx)
	if repo == nil {
		return "", fmt.Errorf("shared repository is not configured")
	}

	user := callCtx.User
	if user == nil || strings.TrimSpace(user.FamilyGroup) == "" || !callCtx.Scope.Valid() {
		return "Family shared features require an active family group. Please ensure your account belongs to a family group.", nil
	}

	switch toolName {
	case "configure_shared_topic":
		return m.executeConfigureSharedTopic(ctx, callCtx, repo, args)
	case "create_shared_reminder":
		return m.executeCreateSharedReminder(ctx, callCtx, repo, args)
	case "complete_shared_reminder":
		return m.executeCompleteSharedReminder(ctx, callCtx, repo, args)
	case "list_shared_activity":
		return m.executeListSharedActivity(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("unknown shared tool: %s", toolName)
	}
}

// SystemPromptSection injects dynamic family sharing context and policies into the prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	user := callCtx.User
	if user == nil || strings.TrimSpace(user.FamilyGroup) == "" || !callCtx.Scope.Valid() {
		return ""
	}

	repo := m.resolveRepo(callCtx)
	var customPolicies []string
	if repo != nil {
		spaces, err := repo.GetSharedSpacesForFamily(callCtx.Scope)
		if err == nil {
			for _, sp := range spaces {
				customPolicies = append(customPolicies, fmt.Sprintf("- Topic '%s': view=%v, edit=%v, notify=%s", sp.Topic, sp.CanView, sp.CanEdit, sp.NotifyMode))
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Family Shared Spaces & Collaboration\nFamily Group: %s\n", callCtx.Scope.LegacyLabel()))
	// V4-27: the default-shared topics the household has (Core plus its packs'), not a fixed list.
	sb.WriteString(fmt.Sprintf("Default shared topics: %s (instant partner notifications).\n", strings.Join(models.DefaultSharedTopics(callCtx.Packs().Topics()), ", ")))
	sb.WriteString("Private by default: personal, work.\n")
	if len(customPolicies) > 0 {
		sb.WriteString("Active configured sharing policies:\n")
		for _, p := range customPolicies {
			sb.WriteString(p + "\n")
		}
	}
	sb.WriteString("When the user logs events, care notes, chores, or other entries under shared topics, cross-user partner notifications are automatically routed.\n")
	sb.WriteString("Use create_shared_reminder and complete_shared_reminder for collaborative tasks and hand-offs.")

	return sb.String()
}

func (m *Module) executeConfigureSharedTopic(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	topic, _ := args["topic"].(string)
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	canView := parseBool(args["can_view"], true)
	canEdit := parseBool(args["can_edit"], true)

	notifyMode, _ := args["notify_mode"].(string)
	notifyMode = strings.ToLower(strings.TrimSpace(notifyMode))
	if notifyMode == "" {
		notifyMode = models.NotifyModeInstant
	}
	if notifyMode != models.NotifyModeInstant && notifyMode != models.NotifyModeDigest && notifyMode != models.NotifyModeSilent {
		notifyMode = models.NotifyModeInstant
	}

	sharedWith, _ := args["shared_with_user_id"].(string)
	sharedWith, err := m.member(ctx, repo, callCtx, sharedWith)
	if err != nil {
		return "", fmt.Errorf("shared_with_user_id: %w", err)
	}

	actorID := callCtx.ActorID()
	familyGroup := callCtx.Scope.LegacyLabel()

	space := models.SharedSpace{
		Topic:            topic,
		OwnerUserID:      actorID,
		SharedWithUserID: sharedWith,
		CanView:          canView,
		CanEdit:          canEdit,
		NotifyMode:       notifyMode,
	}

	if err := repo.UpsertSharedSpace(callCtx.Scope, &space); err != nil {
		return "", fmt.Errorf("failed to configure shared topic: %w", err)
	}

	summary := fmt.Sprintf("Configured sharing for topic '%s' (view: %v, edit: %v, notify: %s)", topic, canView, canEdit, notifyMode)
	m.publishEvent(ctx, callCtx, events.MutationEvent{
		Topic:       topic,
		Action:      "configure",
		EntityType:  "SharedSpace",
		EntityID:    fmt.Sprintf("%d", space.ID),
		ActorUserID: actorID,
		HouseholdID: callCtx.Scope.HouseholdID(),
		FamilyGroup: familyGroup,
		Summary:     summary,
		Timestamp:   time.Now(),
	})

	return fmt.Sprintf("Successfully configured shared topic '%s' for family '%s' (view: %v, edit: %v, notify: %s).",
		topic, familyGroup, canView, canEdit, notifyMode), nil
}

// dueTimeDescription is the due time of both reminder tools (BL-50).
const dueTimeDescription = "When the reminder is due, in ISO 8601 (e.g. '2026-11-03T19:00:00'). A time without an offset is read in the person's own time zone; one with an offset ('Z', '+02:00') is that exact instant."

// urgentDescription is the urgent flag of both reminder tools (V4-05).
const urgentDescription = "Set true only when the reminder must reach the person even during their quiet hours: they ask for that, or it is about medication or safety. Any other reminder due in quiet hours waits until they end. Default false."

func (m *Module) executeScheduleReminder(ctx context.Context, callCtx orchestrator.CallContext, args map[string]interface{}) (string, error) {
	message := toolutil.String(args, "message")
	dueTimeStr := toolutil.String(args, "due_time")

	if message == "" || dueTimeStr == "" {
		return "❌ message and due_time are required to schedule a reminder.", nil
	}

	// BL-50: a time without an offset is the person's local time, not UTC.
	loc := callCtx.Loc()
	dueTime, err := toolutil.ParseTimeInLocation(dueTimeStr, loc)
	if err != nil {
		return "❌ Failed to parse due time. Please use ISO 8601 format.", nil
	}

	actorID := callCtx.ActorID()

	if callCtx.DB == nil {
		return "❌ Database client not configured.", nil
	}

	reminder := models.ScheduledReminder{
		UserID:    actorID,
		Message:   message,
		DueTime:   dueTime,
		IsSent:    false,
		Urgent:    toolutil.Bool(args, "urgent", false),
		CreatedAt: time.Now(),
	}

	if err := callCtx.DB.SaveReminder(reminder); err != nil {
		return fmt.Sprintf("Error scheduling reminder: %v", err), nil
	}

	m.publishEvent(ctx, callCtx, events.MutationEvent{
		Topic:       "shared",
		Action:      "create",
		EntityType:  "ScheduledReminder",
		EntityID:    fmt.Sprintf("%s:%d", actorID, dueTime.Unix()),
		ActorUserID: actorID,
		HouseholdID: callCtx.Scope.HouseholdID(),
		FamilyGroup: callCtx.Scope.LegacyLabel(),
		Summary:     fmt.Sprintf("Scheduled reminder for %s: %s", dueTime.In(loc).Format(time.RFC822), message),
		Timestamp:   time.Now(),
	})

	return fmt.Sprintf("✅ **Reminder Set!**\n\nI will remind you at %s: \"%s\"", dueTime.In(loc).Format(time.RFC822), message), nil
}

func (m *Module) executeCreateSharedReminder(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	topic, _ := args["topic"].(string)
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == "" {
		topic = models.TopicFamily
	}

	assignedTo, _ := args["assigned_to_user_id"].(string)

	// BL-50: a time without an offset is the person's local time, not UTC. A due time that cannot
	// be read leaves the reminder without one, as before.
	loc := callCtx.Loc()
	var dueTime *time.Time
	if dueStr, ok := args["due_time"].(string); ok && strings.TrimSpace(dueStr) != "" {
		if t, err := toolutil.ParseTimeInLocation(dueStr, loc); err == nil {
			dueTime = &t
		}
	}

	assignedTo, err := m.member(ctx, repo, callCtx, assignedTo)
	if err != nil {
		return "", fmt.Errorf("assigned_to_user_id: %w", err)
	}

	actorID := callCtx.ActorID()
	familyGroup := callCtx.Scope.LegacyLabel()

	reminder := models.SharedTaskReminder{
		Topic:            topic,
		Title:            title,
		DueTime:          dueTime,
		CreatedByUserID:  actorID,
		AssignedToUserID: assignedTo,
		IsCompleted:      false,
		Urgent:           toolutil.Bool(args, "urgent", false),
	}

	if err := repo.CreateSharedReminder(callCtx.Scope, &reminder); err != nil {
		return "", fmt.Errorf("failed to create shared reminder: %w", err)
	}

	summary := title
	m.publishEvent(ctx, callCtx, events.MutationEvent{
		Topic:       topic,
		Action:      "create",
		EntityType:  "SharedTaskReminder",
		EntityID:    fmt.Sprintf("%d", reminder.ID),
		ActorUserID: actorID,
		HouseholdID: callCtx.Scope.HouseholdID(),
		FamilyGroup: familyGroup,
		Summary:     summary,
		Timestamp:   time.Now(),
	})

	dueMsg := ""
	if reminder.DueTime != nil {
		dueMsg = fmt.Sprintf(" due at %s", reminder.DueTime.In(loc).Format("2006-01-02 15:04"))
	}

	return fmt.Sprintf("Created shared task/reminder #%d: '%s' under topic '%s'%s for family group '%s'.",
		reminder.ID, reminder.Title, reminder.Topic, dueMsg, familyGroup), nil
}

func (m *Module) executeCompleteSharedReminder(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	idVal, ok := args["id"]
	if !ok {
		return "", fmt.Errorf("id is required")
	}

	id, err := parseUint(idVal)
	if err != nil {
		return "", fmt.Errorf("invalid reminder id: %w", err)
	}

	completedBy := callCtx.ActorID()
	familyGroup := callCtx.Scope.LegacyLabel()

	reminder, err := repo.CompleteSharedReminder(callCtx.Scope, id, completedBy)
	if err != nil {
		return "", fmt.Errorf("failed to complete shared reminder: %w", err)
	}

	// Defence in depth: the repository only finds the household's own tasks (V4-30).
	isAuthorized := reminder != nil && callCtx.Scope.Valid() && reminder.HouseholdID == callCtx.Scope.HouseholdID()
	if !isAuthorized {
		return "", fmt.Errorf("shared reminder with id %d not found or permission denied", id)
	}

	actorName := "Partner"
	if callCtx.User != nil && callCtx.User.Name != "" {
		actorName = callCtx.User.Name
	}

	summary := reminder.Title
	m.publishEvent(ctx, callCtx, events.MutationEvent{
		Topic:       reminder.Topic,
		Action:      "complete",
		EntityType:  "SharedTaskReminder",
		EntityID:    fmt.Sprintf("%d", reminder.ID),
		ActorUserID: completedBy,
		HouseholdID: callCtx.Scope.HouseholdID(),
		FamilyGroup: familyGroup,
		Summary:     summary,
		Timestamp:   time.Now(),
	})

	return fmt.Sprintf("Shared task #%d ('%s') marked as completed by %s.", reminder.ID, reminder.Title, actorName), nil
}

func (m *Module) executeListSharedActivity(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	topicFilter, _ := args["topic"].(string)
	includeCompleted := parseBool(args["include_completed"], false)

	spaces, err := repo.GetSharedSpacesForFamily(callCtx.Scope)
	if err != nil {
		return "", fmt.Errorf("failed to fetch shared spaces: %w", err)
	}

	reminders, err := repo.ListSharedReminders(callCtx.Scope, topicFilter, includeCompleted)
	if err != nil {
		return "", fmt.Errorf("failed to fetch shared reminders: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Shared Family Activity for group '%s':\n\n", callCtx.User.FamilyGroup))

	sb.WriteString("Shared Spaces:\n")
	if len(spaces) == 0 {
		sb.WriteString(fmt.Sprintf("  No custom shared spaces configured. Using default policy (%s shared with instant notifications).\n", strings.Join(models.DefaultSharedTopics(callCtx.Packs().Topics()), ", ")))
	} else {
		for _, sp := range spaces {
			sb.WriteString(fmt.Sprintf("  - [%s] view: %v, edit: %v, notify: %s\n", sp.Topic, sp.CanView, sp.CanEdit, sp.NotifyMode))
		}
	}

	sb.WriteString("\nShared Tasks & Reminders:\n")
	if len(reminders) == 0 {
		sb.WriteString("  No pending tasks found.\n")
	} else {
		for _, r := range reminders {
			status := "⏳ Pending"
			if r.IsCompleted {
				completedBy := r.CompletedByUserID
				if completedBy == "" {
					completedBy = "someone"
				}
				status = fmt.Sprintf("✅ Completed by %s", completedBy)
			}
			due := ""
			if r.DueTime != nil {
				due = fmt.Sprintf(" (Due: %s)", r.DueTime.Format("2006-01-02 15:04"))
			}
			sb.WriteString(fmt.Sprintf("  - #%d [%s] %s %s - %s\n", r.ID, r.Topic, r.Title, due, status))
		}
	}

	return sb.String(), nil
}

// member resolves a tool argument that names another person inside the caller's household
// (ADR 0002 Decision 3). "" and "*" keep their meaning (nobody / everyone in the household).
func (m *Module) member(ctx context.Context, repo Repository, callCtx orchestrator.CallContext, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "*" {
		return ref, nil
	}
	return identity.Member(ctx, repo, callCtx.Scope, ref)
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) Repository {
	if m.repo != nil {
		return m.repo
	}
	if callCtx.Extra != nil {
		if r, ok := callCtx.Extra["shared_repo"].(Repository); ok {
			return r
		}
	}
	if r, ok := callCtx.DB.(Repository); ok {
		return r
	}
	return nil
}

func (m *Module) publishEvent(ctx context.Context, callCtx orchestrator.CallContext, ev events.MutationEvent) {
	bus := m.eventBus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus != nil {
		_ = bus.Publish(ctx, ev)
	}
}

func parseBool(val interface{}, defaultVal bool) bool {
	if val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		if lower == "true" || lower == "1" || lower == "yes" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "no" {
			return false
		}
	}
	return defaultVal
}

func parseUint(val interface{}) (uint, error) {
	switch v := val.(type) {
	case float64:
		return uint(v), nil
	case int:
		return uint(v), nil
	case int64:
		return uint(v), nil
	case uint:
		return v, nil
	case string:
		var u uint64
		_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &u)
		return uint(u), err
	default:
		return 0, fmt.Errorf("invalid integer: %v", val)
	}
}
