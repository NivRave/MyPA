package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/pgvector/pgvector-go"
	"google.golang.org/genai"
)

const ModuleName = "memory"

// Module implements orchestrator.ToolModule for topic-mapped long-term memory
// and living structured entity profiles.
type Module struct {
	repo db.MemoryV2Repository
}

// NewModule constructs a new memory domain Module.
func NewModule(repo db.MemoryV2Repository) *Module {
	return &Module{repo: repo}
}

// Name returns the unique identifier for the memory module.
func (m *Module) Name() string {
	return ModuleName
}

// Declarations returns the Gemini FunctionDeclaration schemas for memory tools.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "save_topic_memory",
			Description: "Saves a structured fact, habit, or preference into long-term topic memory with tags, scope, and temporal tracking. Automatically links or supersedes older conflicting facts when supersedes_memory_id is provided.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Canonical topic domain: one of the household's topics (e.g. 'family', 'health', 'work', 'personal').",
					},
					"subtopic": {
						Type:        genai.TypeString,
						Description: "Subtopic or entity category (e.g. 'pediatrician', 'allergies', 'mortgage', 'routine').",
					},
					"fact": {
						Type:        genai.TypeString,
						Description: "The exact fact or preference to remember.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional list of searchable tags or keywords.",
					},
					"scope": {
						Type:        genai.TypeString,
						Description: "Access scope: 'personal' (only visible to caller) or 'family' (shared across family group). Default is 'personal'.",
					},
					"is_pinned": {
						Type:        genai.TypeBoolean,
						Description: "Whether this memory is pinned for high-priority prompt grounding.",
					},
					"supersedes_memory_id": {
						Type:        genai.TypeInteger,
						Description: "Optional ID of an older memory that this new fact replaces or invalidates.",
					},
				},
				Required: []string{"topic", "fact"},
			},
		},
		{
			Name:        "search_topic_memories",
			Description: "Searches active (non-superseded) topic-mapped long-term memories by topic, text query, and tags.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Optional topic filter: one of the household's topics (e.g. 'family', 'health', 'work', 'personal').",
					},
					"query": {
						Type:        genai.TypeString,
						Description: "Optional keyword or text query matching the fact content or subtopic.",
					},
					"tags": {
						Type: genai.TypeArray,
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
						Description: "Optional tags to filter memories by.",
					},
					"scope": {
						Type:        genai.TypeString,
						Description: "Optional scope filter ('personal' or 'family').",
					},
				},
			},
		},
		{
			Name:        "upsert_entity_profile",
			Description: "Creates or updates a structured living entity profile card (e.g. FamilyMember, Pet, Property, Vehicle) with key-value attributes stored in JSONB.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Canonical topic domain: one of the household's topics (e.g. 'family', 'health', 'work', 'personal').",
					},
					"entity_type": {
						Type:        genai.TypeString,
						Description: "Classification of entity (e.g. 'person', 'pet', 'property', 'vehicle').",
					},
					"entity_name": {
						Type:        genai.TypeString,
						Description: "Name or unique label of the entity (e.g. 'Liam', '123 Main St', 'Tesla Model 3').",
					},
					"attributes": {
						Type:        genai.TypeObject,
						Description: "Structured key-value attributes for this entity (e.g. {'birth_date': '2024-05-10', 'weight_kg': 7.5}).",
					},
				},
				Required: []string{"topic", "entity_type", "entity_name", "attributes"},
			},
		},
		{
			Name:        "get_entity_profile",
			Description: "Retrieves a structured living entity profile card by topic and entity name or type.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"topic": {
						Type:        genai.TypeString,
						Description: "Topic domain: one of the household's topics (e.g. 'family', 'health', 'work', 'personal').",
					},
					"entity_name": {
						Type:        genai.TypeString,
						Description: "Name or label of the entity to fetch.",
					},
				},
				Required: []string{"topic", "entity_name"},
			},
		},
	}
}

// Execute handles tool calls for memory management.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", fmt.Errorf("memory module: %w", err)
	}

	userID := callCtx.ActorID()
	hs := callCtx.Scope // V4-30: the household every memory call is scoped to

	switch toolName {
	case "save_topic_memory":
		return m.executeSaveTopicMemory(ctx, callCtx, repo, userID, hs, args)
	case "search_topic_memories":
		return m.executeSearchTopicMemories(ctx, repo, hs, args)
	case "upsert_entity_profile":
		return m.executeUpsertEntityProfile(ctx, callCtx, repo, userID, hs, args)
	case "get_entity_profile":
		return m.executeGetEntityProfile(ctx, repo, hs, args)
	default:
		return "", fmt.Errorf("unsupported memory tool: %s", toolName)
	}
}

// SystemPromptSection dynamically generates markdown grounding bullet cards for active EntityProfiles
// and pinned topic memories for the caller's family group or user.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return ""
	}

	hs := callCtx.Scope
	profiles, err := repo.ListEntityProfiles(ctx, hs, "")
	if err != nil {
		profiles = nil
	}

	// Fetch pinned memories
	pinnedMemories, err := repo.SearchTopicMemories(ctx, hs, "", "", nil)
	var activePinned []models.TopicMemory
	if err == nil {
		for _, mem := range pinnedMemories {
			if mem.IsPinned && mem.IsActive() {
				activePinned = append(activePinned, mem)
			}
		}
	}

	if len(profiles) == 0 && len(activePinned) == 0 {
		return ""
	}

	var sb strings.Builder
	if len(profiles) > 0 {
		sb.WriteString("### Active Entity Profiles & Grounding\n")
		// Sort profiles by EntityName for stable prompt ordering
		sort.Slice(profiles, func(i, j int) bool {
			return profiles[i].EntityName < profiles[j].EntityName
		})

		for _, p := range profiles {
			sb.WriteString(fmt.Sprintf("- **%s** (%s • %s):\n", p.EntityName, p.EntityType, p.Topic))
			attrs := p.Attributes()
			keys := make([]string, 0, len(attrs))
			for k := range attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				sb.WriteString(fmt.Sprintf("  - %s: %v\n", k, attrs[k]))
			}
		}
	}

	if len(activePinned) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("### Pinned Knowledge\n")
		for _, mem := range activePinned {
			sub := ""
			if mem.Subtopic != "" {
				sub = fmt.Sprintf(" • %s", mem.Subtopic)
			}
			sb.WriteString(fmt.Sprintf("- [%s%s] %s\n", mem.Topic, sub, mem.Fact))
		}
	}

	// P3-1: profiles and pinned memories are stored user data (possibly derived from untrusted
	// input); present them as data, not instructions.
	return orchestrator.WrapUserProvidedData("memory_profiles_and_pins", strings.TrimSpace(sb.String()))
}

// embedFact returns the embedding of a fact, or nil when it cannot be computed (no model wired,
// or the embedding call failed). Per-turn recall finds memories by embedding, so every saved fact
// should have one; but a fact is still worth saving without it (keyword search and pinned
// memories do not need it), so a failure here never fails the save. The fact itself is not logged.
func embedFact(ctx context.Context, callCtx orchestrator.CallContext, fact string) []float32 {
	if callCtx.LLM == nil {
		return nil
	}
	embedding, err := callCtx.LLM.GenerateEmbedding(ctx, fact)
	if err != nil {
		slog.Warn("memory saved without an embedding: it will not be recalled by meaning", "error", err)
		return nil
	}
	return embedding
}

func (m *Module) executeSaveTopicMemory(ctx context.Context, callCtx orchestrator.CallContext, repo db.MemoryV2Repository, userID string, hs identity.Scope, args map[string]interface{}) (string, error) {
	topic := getString(args, "topic")
	subtopic := getString(args, "subtopic")
	fact := getString(args, "fact")
	scope := getString(args, "scope")
	tags := getStringSlice(args, "tags")
	isPinned := getBool(args, "is_pinned")
	supersedesID := getUint(args, "supersedes_memory_id")

	if topic == "" {
		return "", errors.New("parameter 'topic' is required")
	}
	if fact == "" {
		return "", errors.New("parameter 'fact' is required")
	}
	if scope == "" {
		scope = "personal"
	}

	mem := models.TopicMemory{
		UserID:          userID,
		FamilyGroup:     hs.LegacyLabel(),
		Topic:           topic,
		Subtopic:        subtopic,
		Fact:            fact,
		Scope:           scope,
		CreatedByUserID: userID,
		IsPinned:        isPinned,
		ValidFrom:       time.Now(),
		CreatedAt:       time.Now(),
	}
	// The embedding is what per-turn recall searches by (V4-06: remember_fact used to be the
	// only tool that stored one).
	if embedding := embedFact(ctx, callCtx, fact); len(embedding) > 0 {
		mem.Embedding = pgvector.NewVector(embedding)
	}
	mem.SetTags(tags)

	if err := repo.SaveTopicMemory(ctx, hs, &mem); err != nil {
		return "", fmt.Errorf("failed to save topic memory: %w", err)
	}

	// Link and supersede older memory if requested
	if supersedesID > 0 {
		if err := repo.SupersedeMemory(ctx, hs, supersedesID, mem.ID); err != nil {
			// Log error but keep newly created memory
			fmt.Printf("warning: failed to supersede memory %d: %v\n", supersedesID, err)
		}
	}

	// Emit MutationEvent
	if callCtx.EventBus != nil {
		action := "create"
		if supersedesID > 0 {
			action = "supersede"
		}
		afterBytes, _ := json.Marshal(mem)
		_ = callCtx.EventBus.Publish(ctx, events.MutationEvent{
			Topic:       "memory",
			Action:      action,
			EntityType:  "TopicMemory",
			EntityID:    strconv.FormatUint(uint64(mem.ID), 10),
			ActorUserID: userID,
			HouseholdID: hs.HouseholdID(),
			FamilyGroup: hs.LegacyLabel(),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Saved memory [%s] %s: %s", mem.Topic, mem.Subtopic, mem.Fact),
			Timestamp:   time.Now(),
		})
	}

	resp := map[string]interface{}{
		"status":     "saved",
		"id":         mem.ID,
		"topic":      mem.Topic,
		"subtopic":   mem.Subtopic,
		"fact":       mem.Fact,
		"scope":      mem.Scope,
		"is_pinned":  mem.IsPinned,
		"tags":       mem.Tags(),
		"valid_from": mem.ValidFrom.Format(time.RFC3339),
	}
	if supersedesID > 0 {
		resp["superseded_memory_id"] = supersedesID
	}

	out, _ := json.Marshal(resp)
	return string(out), nil
}

func (m *Module) executeSearchTopicMemories(ctx context.Context, repo db.MemoryV2Repository, hs identity.Scope, args map[string]interface{}) (string, error) {
	topic := getString(args, "topic")
	query := getString(args, "query")
	tags := getStringSlice(args, "tags")

	memories, err := repo.SearchTopicMemories(ctx, hs, topic, query, tags)
	if err != nil {
		return "", fmt.Errorf("failed to search topic memories: %w", err)
	}

	type searchItem struct {
		ID        uint     `json:"id"`
		Topic     string   `json:"topic"`
		Subtopic  string   `json:"subtopic,omitempty"`
		Fact      string   `json:"fact"`
		Tags      []string `json:"tags,omitempty"`
		Scope     string   `json:"scope"`
		IsPinned  bool     `json:"is_pinned"`
		CreatedAt string   `json:"created_at"`
	}

	items := make([]searchItem, 0, len(memories))
	for _, mem := range memories {
		items = append(items, searchItem{
			ID:        mem.ID,
			Topic:     mem.Topic,
			Subtopic:  mem.Subtopic,
			Fact:      mem.Fact,
			Tags:      mem.Tags(),
			Scope:     mem.Scope,
			IsPinned:  mem.IsPinned,
			CreatedAt: mem.CreatedAt.Format(time.RFC3339),
		})
	}

	out, _ := json.Marshal(map[string]interface{}{
		"count":    len(items),
		"memories": items,
	})
	return string(out), nil
}

func (m *Module) executeUpsertEntityProfile(ctx context.Context, callCtx orchestrator.CallContext, repo db.MemoryV2Repository, userID string, hs identity.Scope, args map[string]interface{}) (string, error) {
	topic := getString(args, "topic")
	entityType := getString(args, "entity_type")
	entityName := getString(args, "entity_name")
	attrs := getMap(args, "attributes")

	if topic == "" || entityType == "" || entityName == "" {
		return "", errors.New("topic, entity_type, and entity_name are required")
	}

	profile := models.EntityProfile{
		UserID:          userID,
		FamilyGroup:     hs.LegacyLabel(),
		Topic:           topic,
		EntityType:      entityType,
		EntityName:      entityName,
		UpdatedByUserID: userID,
		UpdatedAt:       time.Now(),
	}
	profile.SetAttributes(attrs)

	if err := repo.UpsertEntityProfile(ctx, hs, &profile); err != nil {
		return "", fmt.Errorf("failed to upsert entity profile: %w", err)
	}

	// Emit MutationEvent
	if callCtx.EventBus != nil {
		afterBytes, _ := json.Marshal(profile)
		_ = callCtx.EventBus.Publish(ctx, events.MutationEvent{
			Topic:       "memory",
			Action:      "upsert",
			EntityType:  "EntityProfile",
			EntityID:    strconv.FormatUint(uint64(profile.ID), 10),
			ActorUserID: userID,
			HouseholdID: hs.HouseholdID(),
			FamilyGroup: hs.LegacyLabel(),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated entity profile %s (%s • %s)", profile.EntityName, profile.EntityType, profile.Topic),
			Timestamp:   time.Now(),
		})
	}

	resp := map[string]interface{}{
		"status":      "upserted",
		"id":          profile.ID,
		"topic":       profile.Topic,
		"entity_type": profile.EntityType,
		"entity_name": profile.EntityName,
		"attributes":  profile.Attributes(),
		"updated_at":  profile.UpdatedAt.Format(time.RFC3339),
	}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

func (m *Module) executeGetEntityProfile(ctx context.Context, repo db.MemoryV2Repository, hs identity.Scope, args map[string]interface{}) (string, error) {
	topic := getString(args, "topic")
	entityName := getString(args, "entity_name")

	if topic == "" || entityName == "" {
		return "", errors.New("parameters 'topic' and 'entity_name' are required")
	}

	profile, err := repo.GetEntityProfile(ctx, hs, topic, entityName)
	if err != nil {
		return "", fmt.Errorf("failed to get entity profile: %w", err)
	}
	if profile == nil {
		return `{"status": "not_found", "message": "Entity profile not found"}`, nil
	}

	resp := map[string]interface{}{
		"id":          profile.ID,
		"topic":       profile.Topic,
		"entity_type": profile.EntityType,
		"entity_name": profile.EntityName,
		"attributes":  profile.Attributes(),
		"updated_at":  profile.UpdatedAt.Format(time.RFC3339),
	}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.MemoryV2Repository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if callCtx.Extra != nil {
		if r, ok := callCtx.Extra["memory_repo"].(db.MemoryV2Repository); ok && r != nil {
			return r, nil
		}
	}
	if client, ok := callCtx.DB.(*db.Client); ok && client != nil && client.DB != nil {
		return db.NewMemoryV2Repo(client.DB), nil
	}
	return nil, errors.New("no memory_v2 repository available in CallContext")
}

// Helpers for argument extraction
func getString(args map[string]interface{}, key string) string {
	if val, ok := args[key]; ok {
		if s, ok := val.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func getStringSlice(args map[string]interface{}, key string) []string {
	val, ok := args[key]
	if !ok || val == nil {
		return nil
	}
	if slice, ok := val.([]string); ok {
		return slice
	}
	if ifaceSlice, ok := val.([]interface{}); ok {
		res := make([]string, 0, len(ifaceSlice))
		for _, item := range ifaceSlice {
			if s, ok := item.(string); ok {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" {
					res = append(res, trimmed)
				}
			}
		}
		return res
	}
	return nil
}

func getBool(args map[string]interface{}, key string) bool {
	if val, ok := args[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
		if s, ok := val.(string); ok {
			b, _ := strconv.ParseBool(s)
			return b
		}
	}
	return false
}

func getUint(args map[string]interface{}, key string) uint {
	val, ok := args[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return uint(v)
	case int:
		if v > 0 {
			return uint(v)
		}
	case uint:
		return v
	case string:
		u, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		return uint(u)
	}
	return 0
}

func getMap(args map[string]interface{}, key string) map[string]interface{} {
	val, ok := args[key]
	if !ok || val == nil {
		return make(map[string]interface{})
	}
	if m, ok := val.(map[string]interface{}); ok {
		return m
	}
	return make(map[string]interface{})
}
