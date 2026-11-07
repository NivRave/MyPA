package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/events"
)

// DefaultPromptCacheTTL defines the default 10-minute cache lifetime for prompt sections.
const DefaultPromptCacheTTL = 10 * time.Minute

// PromptCache defines the Redis operations required by the orchestrator for dynamic prompt caching.
type PromptCache interface {
	// GetPromptSection retrieves a single cached module section. Returns (content, found, error).
	GetPromptSection(ctx context.Context, familyGroup, module string) (string, bool, error)

	// GetPromptSections retrieves multiple module prompt sections in a single roundtrip (e.g. via Redis MGet).
	// Returns a map of module names to cached content, and a slice of module names that missed cache.
	GetPromptSections(ctx context.Context, familyGroup string, modules []string) (hits map[string]string, misses []string, err error)

	// SetPromptSection persists a generated prompt section with a specified TTL.
	SetPromptSection(ctx context.Context, familyGroup, module, content string, ttl time.Duration) error

	// InvalidatePromptSections deletes cached prompt sections for multiple Redis keys.
	InvalidatePromptSections(ctx context.Context, keys ...string) error
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

// BuildPromptInvalidationKeys creates the Redis keys to delete for a MutationEvent across tenant scopes.
func BuildPromptInvalidationKeys(event events.MutationEvent) []string {
	modules := MapEventToModules(event)
	if len(modules) == 0 {
		return nil
	}

	scopes := make(map[string]bool)
	if fg := strings.TrimSpace(event.FamilyGroup); fg != "" {
		scopes[fg] = true
	}
	if actor := strings.TrimSpace(event.ActorUserID); actor != "" {
		scopes[actor] = true
	}

	var keys []string
	for scope := range scopes {
		for _, mod := range modules {
			keys = append(keys, fmt.Sprintf("ctx_prompt:%s:%s", scope, mod))
		}
	}
	return keys
}

// RegisterPromptInvalidator attaches a subscriber to EventBus that invalidates Redis prompt cache keys
// whenever a MutationEvent is published on any matching domain topic.
func RegisterPromptInvalidator(bus *events.EventBus, cache PromptCache) {
	if bus == nil || cache == nil {
		return
	}

	bus.SubscribeSync(func(ctx context.Context, ev events.MutationEvent) error {
		keys := BuildPromptInvalidationKeys(ev)
		if len(keys) == 0 {
			return nil
		}
		if err := cache.InvalidatePromptSections(ctx, keys...); err != nil {
			slog.Warn("prompt_cache: failed to invalidate Redis prompt cache keys", "keys", keys, "error", err)
			return nil // Do not halt mutation pipeline on cache invalidation error
		}
		slog.Debug("prompt_cache: invalidated keys on mutation", "keys", keys, "topic", ev.Topic, "action", ev.Action)
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
