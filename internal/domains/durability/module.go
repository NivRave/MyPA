package durability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

func init() {
	db.RegisterModel(
		&models.EncryptedCredential{},
		&models.MutationLog{},
		&models.IdempotencyRecord{},
	)
}

// Repository defines the persistence interface required by the durability module.
type Repository interface {
	SaveEncryptedCredential(cred models.EncryptedCredential) error
	GetEncryptedCredential(userID, provider string) (*models.EncryptedCredential, error)
	InsertMutationLog(log models.MutationLog) error
	GetMutationLogsByTopic(familyGroup, userID, topic string, limit int) ([]models.MutationLog, error)
	GetMutationLogByID(id string) (*models.MutationLog, error)
	GetLastMutation(userID string, familyGroup string) (*models.MutationLog, error)
	MarkMutationUndone(id string) error
	CheckIdempotency(key string) (*models.IdempotencyRecord, error)
	SaveIdempotency(rec models.IdempotencyRecord) error
	ExportTopicData(familyGroup, userID, topic string, format string, limit int) (string, error)
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

	bus.Subscribe(m.HandleMutationEvent)
}

// HandleMutationEvent processes incoming MutationEvents and persists them to the audit log.
func (m *Module) HandleMutationEvent(ctx context.Context, ev events.MutationEvent) error {
	if m.repo == nil {
		return nil
	}

	isUndone := false
	if strings.EqualFold(ev.Action, "undo") {
		isUndone = true
	}

	log := models.MutationLog{
		ID:          ev.ID,
		UserID:      ev.ActorUserID,
		FamilyGroup: ev.FamilyGroup,
		Topic:       ev.Topic,
		Action:      ev.Action,
		EntityType:  ev.EntityType,
		EntityID:    ev.EntityID,
		BeforeJSON:  ev.BeforeJSON,
		AfterJSON:   ev.AfterJSON,
		Summary:     ev.Summary,
		IsUndone:    isUndone,
		CreatedAt:   ev.Timestamp,
	}

	if err := m.repo.InsertMutationLog(log); err != nil {
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
			Name: "undo_last_action",
			Description: "Reverts the user's or family's most recent mutation or change (such as an expense, baby feeding log, schedule entry, or task) using the recorded BeforeJSON state and marks it undone.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"reason": {
						Type:        genai.TypeString,
						Description: "Optional explanation or user-provided reason for the rollback.",
					},
					"mutation_id": {
						Type:        genai.TypeString,
						Description: "Optional specific mutation ID to undo. If omitted, undoes the latest mutation for the active user or family.",
					},
				},
			},
		},
		{
			Name: "list_recent_changes",
			Description: "Lists recent data mutations and changes across life domains (baby, house, finance, schedule, memory). Use when user asks 'what did I change recently?', 'show audit log', or 'who updated what?'.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Filter by topic: 'baby', 'house', 'finance', 'schedule', 'memory', or 'all' (default).",
					},
					"limit": {
						Type:        genai.TypeInteger,
						Description: "Maximum number of recent mutations to return (default 10, max 50).",
					},
				},
			},
		},
		{
			Name: "export_topic_data",
			Description: "Exports structured data and mutation history for a specific topic (baby, house, finance, memory, schedule, or all) as formatted JSON or CSV.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "The topic domain to export: 'baby', 'house', 'finance', 'memory', 'schedule', or 'all'.",
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

func (m *Module) executeUndo(ctx context.Context, callCtx orchestrator.CallContext, repo Repository, args map[string]interface{}) (string, error) {
	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.UserID
	}

	var log *models.MutationLog
	var err error

	if mutID, ok := args["mutation_id"].(string); ok && strings.TrimSpace(mutID) != "" {
		trimmedID := strings.TrimSpace(mutID)
		log, err = repo.GetMutationLogByID(trimmedID)
		if err != nil {
			return "", fmt.Errorf("failed to lookup mutation %s: %w", trimmedID, err)
		}
		if log == nil {
			return "No recent undoable actions found.", nil
		}
		isAuthorized := (familyGroup != "" && log.FamilyGroup == familyGroup) || (log.UserID != "" && log.UserID == userID)
		if !isAuthorized {
			return "", fmt.Errorf("mutation %s not found or permission denied", trimmedID)
		}
	} else {
		log, err = repo.GetLastMutation(userID, familyGroup)
	}

	if err != nil || log == nil {
		return "No recent undoable actions found.", nil
	}

	if log.IsUndone {
		return fmt.Sprintf("Action %s (%s) has already been undone.", log.ID, log.Summary), nil
	}

	var rollbackDetail string
	if handler, exists := m.getUndoHandler(log.EntityType); exists {
		detail, err := handler(ctx, *log, callCtx)
		if err != nil {
			return "", fmt.Errorf("custom undo handler failed for %s: %w", log.EntityType, err)
		}
		rollbackDetail = detail
	} else {
		rollbackDetail = fmt.Sprintf("Reverted %s '%s' on %s (%s).", log.Action, log.EntityID, log.EntityType, log.Summary)
		if log.BeforeJSON != "" && log.BeforeJSON != "{}" {
			rollbackDetail += fmt.Sprintf(" Restored prior state: %s", log.BeforeJSON)
		}
	}

	if err := repo.MarkMutationUndone(log.ID); err != nil {
		return "", fmt.Errorf("failed to mark mutation log undone: %w", err)
	}

	// Publish undo mutation event to event bus if available
	bus := m.bus
	if bus == nil && callCtx.EventBus != nil {
		bus = callCtx.EventBus
	}
	if bus != nil {
		undoEv := events.MutationEvent{
			Topic:       log.Topic,
			Action:      "undo",
			EntityType:  log.EntityType,
			EntityID:    log.EntityID,
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  log.AfterJSON,
			AfterJSON:   log.BeforeJSON,
			Summary:     fmt.Sprintf("Undid %s (%s): %s", log.ID, log.Action, log.Summary),
			Timestamp:   time.Now(),
		}
		_ = bus.Publish(ctx, undoEv)
	}

	return fmt.Sprintf("Successfully undone! %s", rollbackDetail), nil
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

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	logs, err := repo.GetMutationLogsByTopic(familyGroup, userID, topic, limit)
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

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	data, err := repo.ExportTopicData(familyGroup, userID, topic, format, limit)
	if err != nil {
		return "", fmt.Errorf("failed to export topic data: %w", err)
	}

	return data, nil
}

// SystemPromptSection returns runtime instructions regarding undo and audit features.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return `### Durability, Audit & Undo
- All data modifications across life domains (baby care, house contracts, finance, schedules, and memories) are tracked in an immutable mutation log.
- If the user asks to "undo", "cancel that last entry", or "revert that change", use the "undo_last_action" tool.
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
