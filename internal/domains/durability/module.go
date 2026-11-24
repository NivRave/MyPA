package durability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

func init() {
	db.RegisterModel(
		&models.EncryptedCredential{},
		&models.MutationLog{},
		&models.IdempotencyRecord{},
	)
}

// Repository defines the persistence interface required by the durability module. Mutation logs
// are household-scoped (V4-30); credentials and idempotency records are keyed by person or key.
// identity.Store lets the event subscriber build the scope of each event's household.
type Repository interface {
	identity.Store
	SaveEncryptedCredential(cred models.EncryptedCredential) error
	GetEncryptedCredential(userID, provider string) (*models.EncryptedCredential, error)
	InsertMutationLog(s identity.Scope, log models.MutationLog) error
	GetMutationLogsByTopic(s identity.Scope, topic string, limit int) ([]models.MutationLog, error)
	GetMutationLogByID(s identity.Scope, id string) (*models.MutationLog, error)
	GetLastUndoableMutation(s identity.Scope, entityTypes []string) (*models.MutationLog, error)
	MarkMutationUndone(s identity.Scope, id string) error
	RollbackSnapshot(ctx context.Context, s identity.Scope, log models.MutationLog, tableName string, via models.UndoVia) error
	CheckIdempotency(key string) (*models.IdempotencyRecord, error)
	SaveIdempotency(rec models.IdempotencyRecord) error
	ExportTopicData(s identity.Scope, topic string, format string, limit int) (string, error)
}

// RollbackResult provides unified, structured feedback on completed rollback operations.
type RollbackResult = models.RollbackResult

// RollbackEngine is the unified interface exposed to engine, tools, and resolvers. Rollback and
// RollbackLast are chat's (/undo, undo_last_action): the caller's own changes only.
// RollbackFromDashboard is the dashboard's, where an owner may also roll back another member's
// change to the household's shared data (BL-65, db.MayUndo).
type RollbackEngine interface {
	Rollback(ctx context.Context, s identity.Scope, mutationID string, reason ...string) (*RollbackResult, error)
	RollbackLast(ctx context.Context, s identity.Scope, reason ...string) (*RollbackResult, error)
	RollbackFromDashboard(ctx context.Context, s identity.Scope, mutationID string) (*RollbackResult, error)
}

// UndoHandler defines a custom rollback function for a specific EntityType.
type UndoHandler func(ctx context.Context, log models.MutationLog, callCtx orchestrator.CallContext) (string, error)

// Module implements the orchestrator.ToolModule interface for durability,
// audit logs, state recovery, and undo/rollback operations.
type Module struct {
	repo         Repository
	bus          *events.EventBus
	mu           sync.RWMutex
	undoHandlers map[string]UndoHandler
}

// NewModule creates and initializes a new durability ToolModule.
// If an EventBus is provided, the module subscribes to mutation events automatically.
func NewModule(repo Repository, bus ...*events.EventBus) *Module {
	m := &Module{
		repo:         repo,
		undoHandlers: make(map[string]UndoHandler),
	}
	if len(bus) > 0 && bus[0] != nil {
		m.AttachEventBus(bus[0])
	}
	return m
}

// Name returns the unique identifier for the durability module.
func (m *Module) Name() string {
	return "durability"
}

// AttachEventBus subscribes the module to all published MutationEvents on the bus.
func (m *Module) AttachEventBus(bus *events.EventBus) {
	if bus == nil {
		return
	}
	m.mu.Lock()
	m.bus = bus
	m.mu.Unlock()

	bus.SubscribeSync(m.HandleMutationEvent)
}

// HandleMutationEvent processes incoming MutationEvents and persists them to the audit log of the
// event's household. The scope is built from the event's stored household (ADR 0002 Decision 3);
// an event without a usable household is not logged.
func (m *Module) HandleMutationEvent(ctx context.Context, ev events.MutationEvent) error {
	if m.repo == nil {
		return nil
	}

	scope, err := identity.ForStoredRow(ctx, m.repo, ev.HouseholdID, ev.ActorUserID)
	if err != nil {
		slog.Warn("durability: mutation event has no usable household; not logged", "id", ev.ID, "topic", ev.Topic, "error", err)
		return nil
	}

	isUndone := strings.EqualFold(ev.Action, "undo")

	log := models.MutationLog{
		ID:         ev.ID,
		UserID:     ev.ActorUserID,
		Topic:      ev.Topic,
		Action:     ev.Action,
		EntityType: ev.EntityType,
		EntityID:   ev.EntityID,
		BeforeJSON: ev.BeforeJSON,
		AfterJSON:  ev.AfterJSON,
		Summary:    ev.Summary,
		IsUndone:   isUndone,
		CreatedAt:  ev.Timestamp,
	}

	if err := m.repo.InsertMutationLog(scope, log); err != nil {
		slog.Error("durability: failed to insert mutation log", "error", err, "id", ev.ID, "topic", ev.Topic)
		return err
	}

	return nil
}

// RegisterUndoHandler registers custom rollback logic for a specific entity type.
func (m *Module) RegisterUndoHandler(entityType string, handler UndoHandler) {
	if handler == nil || strings.TrimSpace(entityType) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.undoHandlers[strings.ToLower(strings.TrimSpace(entityType))] = handler
}

func (m *Module) getUndoHandler(entityType string) (UndoHandler, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.undoHandlers[strings.ToLower(strings.TrimSpace(entityType))]
	return h, ok
}

// SaveCredentialFallback encrypts tokenData using AES-256-GCM and persists it as a durable fallback.
func (m *Module) SaveCredentialFallback(ctx context.Context, userID, provider string, tokenData []byte, passphrase ...string) error {
	if m.repo == nil {
		return fmt.Errorf("durability: repository is not configured")
	}
	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	ciphertext, err := crypto.Encrypt(tokenData, pw)
	if err != nil {
		return fmt.Errorf("failed to encrypt credential fallback: %w", err)
	}
	cred := models.EncryptedCredential{
		UserID:     userID,
		Provider:   provider,
		Ciphertext: ciphertext,
		UpdatedAt:  time.Now(),
	}
	return m.repo.SaveEncryptedCredential(cred)
}

// GetCredentialFallback retrieves and decrypts a persistent credential fallback.
func (m *Module) GetCredentialFallback(ctx context.Context, userID, provider string, passphrase ...string) ([]byte, error) {
	if m.repo == nil {
		return nil, fmt.Errorf("durability: repository is not configured")
	}
	cred, err := m.repo.GetEncryptedCredential(userID, provider)
	if err != nil {
		return nil, err
	}
	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	plaintext, err := crypto.Decrypt(cred.Ciphertext, pw)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt credential fallback: %w", err)
	}
	return plaintext, nil
}

// Declarations returns the Gemini FunctionDeclaration schemas for durability tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "undo_last_action",
			Description: "Reverts the user's own most recent change that can be undone (such as a saved memory, schedule entry, reminder, or task) using the recorded BeforeJSON state and marks it undone. Another member's change is theirs to undo.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"reason": {
						Type:        genai.TypeString,
						Description: "Optional explanation or user-provided reason for the rollback.",
					},
					"mutation_id": {
						Type:        genai.TypeString,
						Description: "Optional specific mutation ID to undo; it must be one of the user's own changes. If omitted, undoes the user's own latest change that can be undone.",
					},
				},
			},
		},
		{
			Name:        "list_recent_changes",
			Description: "Lists recent data mutations and changes across life domains (the household's topics, schedule, memory). Use when user asks 'what did I change recently?', 'show audit log', or 'who updated what?'.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Filter by topic: one of the household's topics, 'schedule', 'memory', or 'all' (default).",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Maximum number of recent mutations to return (default 10, max 50).",
					},
				},
			},
		},
		{
			Name:        "export_topic_data",
			Description: "Exports structured data and mutation history for a specific topic (one of the household's topics, memory, schedule, or all) as formatted JSON or CSV.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "The topic domain to export: one of the household's topics, 'memory', 'schedule', or 'all'.",
					},
					"format": {
						Type:        genai.TypeString,
						Description: "The export format: 'json' (default) or 'csv'.",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Maximum number of records to export (default 100).",
					},
				},
				Required: []string{"topic"},
			},
		},
	}
}

// resolveRepo determines the active Repository from the module or call context.
func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (Repository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(Repository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["durability_repo"].(Repository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("durability: no repository available in module or call context")
}

// Execute dispatches and runs tools provided by the durability module.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "undo_last_action":
		return m.executeUndo(ctx, callCtx, repo, args)
	case "list_recent_changes":
		return m.executeListRecentChanges(ctx, callCtx, repo, args)
	case "export_topic_data":
		return m.executeExportTopicData(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("durability: unknown tool %q", toolName)
	}
}

// Rollback performs atomic, snapshot-backed rollback, asked in chat, of a specific mutation ID of
// the scope's household: one of the caller's own changes (BL-65). A mutation of another household
// is refused exactly like one that does not exist, so the answer does not reveal whether it
// exists. An empty ID is RollbackLast.
func (m *Module) Rollback(ctx context.Context, s identity.Scope, mutationID string, reason ...string) (*RollbackResult, error) {
	return m.rollbackByID(ctx, s, mutationID, models.UndoViaChat, reason...)
}

// RollbackFromDashboard is the dashboard's rollbackMutation: like Rollback, except that an owner
// may also roll back another member's change, or one with no known person, to the household's
// shared data (BL-65). A person-owned row is still rolled back by its own person only.
func (m *Module) RollbackFromDashboard(ctx context.Context, s identity.Scope, mutationID string) (*RollbackResult, error) {
	if strings.TrimSpace(mutationID) == "" {
		return nil, fmt.Errorf("a mutation ID is required")
	}
	return m.rollbackByID(ctx, s, mutationID, models.UndoViaDashboard)
}

func (m *Module) rollbackByID(ctx context.Context, s identity.Scope, mutationID string, via models.UndoVia, reason ...string) (*RollbackResult, error) {
	if m.repo == nil {
		return nil, fmt.Errorf("durability: repository is not configured")
	}
	if !s.Valid() {
		return nil, fmt.Errorf("permission denied: %w", identity.ErrNoScope)
	}

	trimmedID := strings.TrimSpace(mutationID)
	if trimmedID == "" {
		return m.RollbackLast(ctx, s, reason...)
	}

	log, err := m.repo.GetMutationLogByID(s, trimmedID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to lookup mutation %s: %w", trimmedID, err)
	}
	if log == nil {
		return nil, fmt.Errorf("permission denied: mutation %q is not a mutation of this household", trimmedID)
	}

	return m.executeRollbackInternal(ctx, s, log, via)
}

// undoableEntityTypes are the entity types undo can roll back: the allowlist (BL-47).
func undoableEntityTypes() []string {
	out := make([]string, 0, len(AllowedRollbackEntities))
	for t := range AllowedRollbackEntities {
		out = append(out, t)
	}
	return out
}

// RollbackLast performs atomic, snapshot-backed rollback, asked in chat (/undo), of the caller's
// own newest change that can be undone (BL-65). Changes of entity types undo cannot roll back are
// skipped, so one such entry never blocks undo (BL-47).
func (m *Module) RollbackLast(ctx context.Context, s identity.Scope, reason ...string) (*RollbackResult, error) {
	if m.repo == nil {
		return nil, fmt.Errorf("durability: repository is not configured")
	}
	if !s.Valid() {
		return nil, fmt.Errorf("permission denied: %w", identity.ErrNoScope)
	}

	log, err := m.repo.GetLastUndoableMutation(s, undoableEntityTypes())
	if err != nil || log == nil {
		return nil, fmt.Errorf("no recent undoable actions found")
	}

	return m.executeRollbackInternal(ctx, s, log, models.UndoViaChat)
}

func (m *Module) executeRollbackInternal(ctx context.Context, s identity.Scope, log *models.MutationLog, via models.UndoVia) (*RollbackResult, error) {
	// Tenant boundary check (V4-30): the log must belong to the scope's household. The repository
	// already confines the lookup; this is the second fence.
	if !s.Valid() || log.HouseholdID != s.HouseholdID() {
		return nil, fmt.Errorf("permission denied: mutation %q belongs to another tenant", log.ID)
	}
	// Whose change it is (BL-65), before anything about it is answered. The repository checks it
	// again on the locked log, with the row it touches.
	if err := db.MayUndo(s, via, *log, ""); err != nil {
		return nil, err
	}
	actorID := s.PersonKey()

	if log.IsUndone {
		return &RollbackResult{
			MutationID:    log.ID,
			Topic:         log.Topic,
			Action:        log.Action,
			EntityType:    log.EntityType,
			EntityID:      log.EntityID,
			Summary:       log.Summary,
			Detail:        fmt.Sprintf("Action %s (%s) has already been undone.", log.ID, log.Summary),
			AlreadyUndone: true,
		}, nil
	}

	// Validate against strict entity allowlist
	tbl, err := ResolveRollbackTable(log.EntityType)
	if err != nil {
		return nil, err
	}

	var detail string
	// Check if a domain registered a custom UndoHandler (e.g. external Google Calendar deletion)
	if handler, exists := m.getUndoHandler(log.EntityType); exists {
		// A custom handler sees no row; the snapshots say whose it was and would be. Another
		// person's change without either snapshot cannot be judged, so it is refused.
		before, after := snapshotState(log.BeforeJSON), snapshotState(log.AfterJSON)
		if before == nil && after == nil && !db.MutationIsOwn(s, *log) {
			return nil, fmt.Errorf("permission denied: mutation %q is not one of your changes and has no snapshot to check", log.ID)
		}
		if err := db.MayUndo(s, via, *log, tbl, before, after); err != nil {
			return nil, err
		}
		callCtx := orchestrator.CallContext{
			User:     &models.User{PlatformID: actorID, HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel()},
			Scope:    s,
			Message:  channel.Inbound{Sender: actorID},
			EventBus: m.bus,
		}
		customDetail, err := handler(ctx, *log, callCtx)
		if err != nil {
			return nil, fmt.Errorf("custom undo handler failed for %s: %w", log.EntityType, err)
		}
		detail = customDetail
		if err := m.repo.MarkMutationUndone(s, log.ID); err != nil {
			return nil, fmt.Errorf("failed to mark mutation log undone: %w", err)
		}
	} else {
		// Default: Atomic Transactional Snapshot Rollback
		if err := m.repo.RollbackSnapshot(ctx, s, *log, tbl, via); err != nil {
			if strings.HasPrefix(err.Error(), "permission denied") {
				return nil, err
			}
			return nil, fmt.Errorf("snapshot rollback failed: %w", err)
		}
		detail = fmt.Sprintf("Reverted %s '%s' on %s.", log.Action, log.EntityID, log.EntityType)
		if before := withoutVector(tbl, log.BeforeJSON); before != "" && before != "{}" {
			detail += fmt.Sprintf(" Restored prior state: %s", before)
		}
	}

	// Publish MutationEvent with Action: "undo"
	if m.bus != nil {
		_ = m.bus.Publish(ctx, events.MutationEvent{
			Topic:       log.Topic,
			Action:      "undo",
			EntityType:  log.EntityType,
			EntityID:    log.EntityID,
			ActorUserID: actorID,
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			BeforeJSON:  withoutVector(tbl, log.AfterJSON),
			AfterJSON:   withoutVector(tbl, log.BeforeJSON),
			Summary:     fmt.Sprintf("Undid %s (%s): %s", log.ID, log.Action, log.Summary),
			Timestamp:   time.Now(),
		})
	}

	return &RollbackResult{
		MutationID:    log.ID,
		Topic:         log.Topic,
		Action:        log.Action,
		EntityType:    log.EntityType,
		EntityID:      log.EntityID,
		Summary:       log.Summary,
		Detail:        detail,
		AlreadyUndone: false,
	}, nil
}

// snapshotState reads a snapshot for db.MayUndo; an empty or unreadable one says nothing.
func snapshotState(js string) db.UndoRowState {
	var m map[string]interface{}
	if strings.TrimSpace(js) == "" || json.Unmarshal([]byte(js), &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// withoutVector drops the embedding from a memory snapshot written before BL-25, so an undo's own
// log entry and its reply never carry the old vector again. Other snapshots are returned as they
// are.
func withoutVector(table, js string) string {
	if table != "topic_memories" || !strings.Contains(js, `"embedding"`) {
		return js
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		return js
	}
	delete(m, "embedding")
	out, err := json.Marshal(m)
	if err != nil {
		return js
	}
	return string(out)
}

func (m *Module) executeUndo(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	mutID, _ := args["mutation_id"].(string)
	res, err := m.Rollback(ctx, callCtx.Scope, mutID)
	if err != nil {
		if strings.Contains(err.Error(), "no recent undoable actions found") {
			return "No recent undoable actions found.", nil
		}
		return "", err
	}
	if res.AlreadyUndone {
		return res.Detail, nil
	}
	return fmt.Sprintf("Successfully undone! %s", res.Detail), nil
}

func (m *Module) executeListRecentChanges(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	topic := "all"
	if t, ok := args["topic"].(string); ok && strings.TrimSpace(t) != "" {
		topic = strings.TrimSpace(t)
	}

	limit := 10
	if l, ok := args["limit"]; ok {
		limit = parseInt(l, 10)
	}
	if limit > 50 {
		limit = 50
	}

	logs, err := repo.GetMutationLogsByTopic(callCtx.Scope, topic, limit)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve recent changes: %w", err)
	}

	if len(logs) == 0 {
		return fmt.Sprintf("No recent changes recorded for topic %q.", topic), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recent Changes (Topic: %s, %d items):\n", topic, len(logs)))
	for i, l := range logs {
		status := "Active"
		if l.IsUndone {
			status = "UNDONE"
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] [%s] %s — %s (ID: %s, Entity: %s/%s, Status: %s)\n",
			i+1,
			l.CreatedAt.Format("2006-01-02 15:04:05"),
			strings.ToUpper(l.Topic),
			strings.ToUpper(l.Action),
			l.Summary,
			l.ID,
			l.EntityType,
			l.EntityID,
			status,
		))
	}

	return sb.String(), nil
}

func (m *Module) executeExportTopicData(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	topic, _ := args["topic"].(string)
	if strings.TrimSpace(topic) == "" {
		topic = "all"
	}

	format := "json"
	if f, ok := args["format"].(string); ok && strings.TrimSpace(f) != "" {
		format = strings.ToLower(strings.TrimSpace(f))
	}

	limit := 100
	if l, ok := args["limit"]; ok {
		limit = parseInt(l, 100)
	}

	data, err := repo.ExportTopicData(callCtx.Scope, topic, format, limit)
	if err != nil {
		return "", fmt.Errorf("failed to export topic data: %w", err)
	}

	return data, nil
}

// SystemPromptSection returns runtime instructions regarding undo and audit features.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return `### Durability, Audit & Undo
- All data modifications across life domains (every topic of the household, schedules, and memories) are tracked in an immutable mutation log.
- If the user asks to "undo", "cancel that last entry", or "revert that change", use the "undo_last_action" tool. It undoes the user's own changes only; another member undoes theirs themselves.
- You can inspect change history with "list_recent_changes" and export domain records with "export_topic_data".`
}

func parseInt(val interface{}, defaultVal int) int {
	if val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
	}
	return defaultVal
}
