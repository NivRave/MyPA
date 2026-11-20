package orchestrator

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/events"
)

// DefaultPromptCacheTTL defines the default 10-minute cache lifetime for prompt sections.
const DefaultPromptCacheTTL = 10 * time.Minute

// PromptCache defines the Redis operations required by the orchestrator for dynamic prompt caching.
// Sections are cached PER USER (household ID + userID + module, V4-30): several modules render personal
// data (pinned memories, persona, standing instructions), so a family-wide key would leak them.
type PromptCache interface {
	// GetPromptSection retrieves a single cached module section. Returns (content, found, error).
	GetPromptSection(ctx context.Context, householdID, userID, module string) (string, bool, error)

	// GetPromptSections retrieves multiple module prompt sections in a single roundtrip (e.g. via Redis MGet).
	// Returns a map of module names to cached content, and a slice of module names that missed cache.
	GetPromptSections(ctx context.Context, householdID, userID string, modules []string) (hits map[string]string, misses []string, err error)

	// SetPromptSection persists a generated prompt section with a specified TTL.
	SetPromptSection(ctx context.Context, householdID, userID, module, content string, ttl time.Duration) error

	// InvalidateFamilyPromptSections deletes the given modules' sections for every user in a scope.
	InvalidateFamilyPromptSections(ctx context.Context, scope string, modules []string) (int, error)
}

// MapEventToModules resolves which ToolModule names are invalidated by a MutationEvent.
func MapEventToModules(event events.MutationEvent) []string {
	topic := strings.ToLower(strings.TrimSpace(event.Topic))
	entity := strings.ToLower(strings.TrimSpace(event.EntityType))

	var mods []string
	switch topic {
	case "baby":
		mods = append(mods, "baby")
	case "house":
		mods = append(mods, "house")
	case "finance":
		mods = append(mods, "finance")
	case "memory":
		mods = append(mods, "memory")
	case "schedule", "calendar":
		mods = append(mods, "schedule", "workspace_calendar")
	case "shared":
		mods = append(mods, "shared")
	case "automation":
		mods = append(mods, "automation")
	case "durability":
		mods = append(mods, "durability")
	case "tasks", "workspace_tasks":
		mods = append(mods, "workspace_tasks")
	case "gmail", "workspace_gmail":
		mods = append(mods, "workspace_gmail")
	case "contacts", "workspace_contacts":
		mods = append(mods, "workspace_contacts")
	case "web_research":
		mods = append(mods, "web_research")
	default:
		if topic != "" {
			mods = append(mods, topic)
		}
	}

	// Resilient fallback for GraphQL mutations where topic might be a subtopic (e.g. topic: "health", entity: "topic_memory")
	if strings.Contains(entity, "memory") || strings.Contains(entity, "entityprofile") || strings.Contains(entity, "entity_profile") {
		if !containsString(mods, "memory") {
			mods = append(mods, "memory")
		}
	}

	return mods
}

// BuildPromptInvalidationScopes returns the tenant scopes (first key segment) and modules a
// MutationEvent invalidates. The scope is the event's household (V4-30): every user has one, so
// there is no per-user fallback scope any more. Every user within it is invalidated, because a
// family-shared mutation (e.g. a family pinned memory) changes every member's rendered section.
func BuildPromptInvalidationScopes(event events.MutationEvent) (scopes []string, modules []string) {
	modules = MapEventToModules(event)
	if len(modules) == 0 {
		return nil, nil
	}
	if s := strings.TrimSpace(event.HouseholdID); s != "" {
		scopes = append(scopes, s)
	}
	return scopes, modules
}

// RegisterPromptInvalidator attaches a subscriber to EventBus that invalidates Redis prompt cache keys
// whenever a MutationEvent is published on any matching domain topic.
func RegisterPromptInvalidator(bus *events.EventBus, cache PromptCache) {
	if bus == nil || cache == nil {
		return
	}

	bus.SubscribeSync(func(ctx context.Context, ev events.MutationEvent) error {
		scopes, modules := BuildPromptInvalidationScopes(ev)
		for _, scope := range scopes {
			n, err := cache.InvalidateFamilyPromptSections(ctx, scope, modules)
			if err != nil {
				slog.Warn("prompt_cache: failed to invalidate Redis prompt cache keys", "scope", scope, "modules", modules, "error", err)
				continue // Do not halt mutation pipeline on cache invalidation error
			}
			slog.Debug("prompt_cache: invalidated keys on mutation", "scope", scope, "modules", modules, "deleted", n, "topic", ev.Topic, "action", ev.Action)
		}
		return nil
	})
}

func containsString(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
