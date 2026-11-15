package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"
)

// NotificationSender defines a callback to send proactive or partner messages.
type NotificationSender func(ctx context.Context, userID, text string) error

// CallContext provides domain ToolModules with all ambient context and services
// required to execute tools and generate system prompt additions.
type CallContext struct {
	User                *models.User
	Message             models.Message
	DB                  DBClient
	LLM                 LLMClient
	EventBus            *events.EventBus
	SendNotification    NotificationSender
	Extra               map[string]interface{}
	HasUntrustedContent bool
	UntrustedSources    []string
}

// ActorID returns the canonical external platform identifier (e.g. Telegram chat ID,
// WhatsApp phone number) representing the active actor.
func (c CallContext) ActorID() string {
	if c.User != nil && strings.TrimSpace(c.User.PlatformID) != "" {
		return strings.TrimSpace(c.User.PlatformID)
	}
	return strings.TrimSpace(c.Message.UserID)
}

// TenantFamilyGroup returns the non-empty family group scope for the active session.
// If the user does not belong to a family group (or User is nil), it safely falls
// back to ActorID(), ensuring a non-empty, strictly scoped tenant boundary.
func (c CallContext) TenantFamilyGroup() string {
	if c.User != nil && strings.TrimSpace(c.User.FamilyGroup) != "" {
		return strings.TrimSpace(c.User.FamilyGroup)
	}
	return c.ActorID()
}

// ToolModule is the unified contract that domain packages (e.g. durability, memory,
// shared, schedule, baby, house, finance, automation) implement.
type ToolModule interface {
	// Name returns the unique identifier for the module (e.g. "baby", "durability").
	Name() string

	// Declarations returns the Gemini FunctionDeclaration schemas provided by this module.
	Declarations() []*genai.FunctionDeclaration
	Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error)

	// SystemPromptSection returns dynamic system instructions for this module,
	// such as baby age, active shared spaces, living profiles, or current budgets.
	SystemPromptSection(ctx context.Context, callCtx CallContext) string
}

// Registry manages the set of registered ToolModules and dispatches tool execution.
type Registry struct {
	mu          sync.RWMutex
	modules     []ToolModule
	byName      map[string]ToolModule
	byTool      map[string]ToolModule
	promptCache PromptCache
}

// NewRegistry initializes an empty ToolModule Registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make([]ToolModule, 0),
		byName:  make(map[string]ToolModule),
		byTool:  make(map[string]ToolModule),
	}
}

// WithPromptCache configures the Redis prompt cache on the Registry.
func (r *Registry) WithPromptCache(cache PromptCache) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.promptCache = cache
	return r
}

// Register adds or updates a ToolModule in the registry.
func (r *Registry) Register(module ToolModule) {
	if module == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	name := module.Name()
	if _, exists := r.byName[name]; !exists {
		r.modules = append(r.modules, module)
	} else {
		for i, m := range r.modules {
			if m.Name() == name {
				r.modules[i] = module
				break
			}
		}
	}
	r.byName[name] = module

	for _, decl := range module.Declarations() {
		if decl != nil && decl.Name != "" {
			r.byTool[decl.Name] = module
		}
	}
}

// GetModule retrieves a registered module by its name.
func (r *Registry) GetModule(name string) (ToolModule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byName[name]
	return m, ok
}

// GetModuleForTool returns the registered ToolModule handling toolName.
func (r *Registry) GetModuleForTool(toolName string) (ToolModule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mod, ok := r.byTool[toolName]
	return mod, ok
}

// Modules returns a snapshot of all registered modules.
func (r *Registry) Modules() []ToolModule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolModule, len(r.modules))
	copy(out, r.modules)
	return out
}

// AllDeclarations collects and returns all Gemini FunctionDeclarations from all registered modules.
func (r *Registry) AllDeclarations() []*genai.FunctionDeclaration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.allDeclarationsLocked()
}

// allDeclarationsLocked collects declarations from registered modules.
// Caller must hold r.mu (RLock or Lock).
func (r *Registry) allDeclarationsLocked() []*genai.FunctionDeclaration {
	var decls []*genai.FunctionDeclaration
	seen := make(map[string]bool)
	for _, m := range r.modules {
		for _, d := range m.Declarations() {
			if d != nil && d.Name != "" && !seen[d.Name] {
				seen[d.Name] = true
				decls = append(decls, d)
			}
		}
	}
	return decls
}

// Execute finds the registered module handling toolName and executes it.
// Returns:
// - result: the execution output string
// - handled: true if a registered module matched toolName, false otherwise
// - err: any execution error returned by the module
func (r *Registry) Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, bool, error) {
	if toolName == "get_current_time" {
		loc := time.UTC
		if args != nil {
			if tzVal, ok := args["timezone"].(string); ok && strings.TrimSpace(tzVal) != "" {
				if l, err := time.LoadLocation(strings.TrimSpace(tzVal)); err == nil {
					loc = l
				}
			}
		}
		now := time.Now().In(loc)
		resp := map[string]interface{}{
			"current_time": now.Format(time.RFC3339),
			"timezone":     loc.String(),
			"day_of_week":  now.Weekday().String(),
			"unix":         now.Unix(),
		}
		b, _ := json.Marshal(resp)
		return string(b), true, nil
	}

	r.mu.RLock()
	mod, ok := r.byTool[toolName]
	r.mu.RUnlock()

	if !ok {
		return "", false, nil
	}

	result, err := mod.Execute(ctx, callCtx, toolName, args)
	return result, true, err
}

// CombinedSystemPrompt iterates through registered modules and aggregates their
// SystemPromptSection outputs into a single formatted string, leveraging Redis caching
// and concurrent errgroup.Group evaluation with an 800ms bounded timeout on cache misses.
func (r *Registry) CombinedSystemPrompt(ctx context.Context, callCtx CallContext) string {
	r.mu.RLock()
	mods := make([]ToolModule, len(r.modules))
	copy(mods, r.modules)
	cache := r.promptCache
	r.mu.RUnlock()

	if len(mods) == 0 {
		return ""
	}

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	// Cache only when the caller's identity is known: sections may contain personal data,
	// so they are keyed per user and must never be stored under a shared/anonymous key.
	useCache := cache != nil && familyGroup != "" && userID != ""
	results := make([]string, len(mods))
	var missingIndices []int

	// 1. Check Redis Cache via MGet
	if useCache {
		modNames := make([]string, len(mods))
		for i, m := range mods {
			modNames[i] = m.Name()
		}

		hits, _, err := cache.GetPromptSections(ctx, familyGroup, userID, modNames)
		if err == nil {
			for i, m := range mods {
				if val, ok := hits[m.Name()]; ok {
					results[i] = val
				} else {
					missingIndices = append(missingIndices, i)
				}
			}
		} else {
			slog.Warn("prompt_cache: Redis MGet failed, falling back to concurrent evaluation", "error", err)
			for i := range mods {
				missingIndices = append(missingIndices, i)
			}
		}
	} else {
		for i := range mods {
			missingIndices = append(missingIndices, i)
		}
	}

	// 2. All sections hit the cache: return immediately (< 1ms latency!)
	if len(missingIndices) == 0 {
		return joinNonEmptySections(results)
	}

	// 3. On cache miss, execute missing module sections concurrently using errgroup.Group bounded to 800ms
	ctx800, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	var g errgroup.Group
	var toCache sync.Map // map[string]string for Redis batch caching

	for _, idx := range missingIndices {
		i := idx
		m := mods[i]

		g.Go(func() error {
			done := make(chan string, 1)
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						slog.Error("recovered panic in module SystemPromptSection",
							"module", m.Name(),
							"family_group", familyGroup,
							"panic", rec,
						)
						done <- ""
					}
				}()
				done <- m.SystemPromptSection(ctx800, callCtx)
			}()

			select {
			case sec := <-done:
				trimmed := strings.TrimSpace(sec)
				results[i] = trimmed
				toCache.Store(m.Name(), trimmed)
			case <-ctx800.Done():
				slog.Warn("prompt_cache: module prompt section timed out, proceeding with remaining sections",
					"module", m.Name(),
					"timeout", "800ms",
					"family_group", familyGroup,
				)
			}
			return nil
		})
	}

	// Wait for all missing modules to complete or timeout
	_ = g.Wait()

	// 4. Asynchronously persist freshly generated prompt sections to Redis with 10-minute TTL
	if useCache {
		toCache.Range(func(key, value any) bool {
			modName := key.(string)
			content := value.(string)
			if err := cache.SetPromptSection(ctx, familyGroup, userID, modName, content, DefaultPromptCacheTTL); err != nil {
				slog.Warn("prompt_cache: failed to write prompt section to Redis", "module", modName, "error", err)
			}
			return true
		})
	}

	return joinNonEmptySections(results)
}

func joinNonEmptySections(sections []string) string {
	var finalSections []string
	for _, s := range sections {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			finalSections = append(finalSections, trimmed)
		}
	}
	if len(finalSections) == 0 {
		return ""
	}
	return strings.Join(finalSections, "\n\n")
}

// CoreToolNames defines the Tier 1 tools that are ALWAYS injected into every turn.
var CoreToolNames = map[string]bool{
	"remember_fact":         true,
	"save_topic_memory":     true,
	"search_topic_memories": true,
	"schedule_reminder":     true,
	"search_web":            true,
	"fetch_webpage":         true,
	"undo_last_action":      true,
	"get_current_time":      true,
}

// ModuleDomainMapping maps each registered module name to its default Tier 2 DomainGroup.
var ModuleDomainMapping = map[string]DomainGroup{
	"baby":               DomainBaby,
	"finance":            DomainFinance,
	"house":              DomainHouse,
	"workspace_calendar": DomainCalendar,
	"schedule":           DomainCalendar,
	"workspace_gmail":    DomainGmail,
	"workspace_tasks":    DomainTasks,
	"workspace_contacts": DomainContacts,
	"automation":         DomainAutomation,
	"shared":             DomainShared,
	// Core tools of these modules (remember_fact, save_topic_memory, search_topic_memories,
	// undo_last_action) are always offered; the mapping exposes the remaining ones.
	"memory":     DomainMemory,
	"durability": DomainHistory,
}

// CoreDeclarations returns only the Tier 1 core function declarations.
func (r *Registry) CoreDeclarations() []*genai.FunctionDeclaration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.coreDeclarationsLocked()
}

// coreDeclarationsLocked returns Tier 1 core function declarations.
// Caller must hold r.mu (RLock or Lock).
func (r *Registry) coreDeclarationsLocked() []*genai.FunctionDeclaration {
	var coreDecls []*genai.FunctionDeclaration
	seen := make(map[string]bool)

	for _, m := range r.modules {
		for _, d := range m.Declarations() {
			if d != nil && CoreToolNames[d.Name] && !seen[d.Name] {
				seen[d.Name] = true
				coreDecls = append(coreDecls, d)
			}
		}
	}

	// Ensure get_current_time schema is present if not provided by a module
	if !seen["get_current_time"] {
		coreDecls = append(coreDecls, getCurrentTimeDeclaration())
	}

	return coreDecls
}

// SelectDeclarations returns Tier 1 core tools + Tier 2 declarations for the specified domain groups.
// If matchedDomains is empty, fallbackToAll determines whether all tools or default ambient tools are returned.
func (r *Registry) SelectDeclarations(matchedDomains []DomainGroup, fallbackToAll bool) []*genai.FunctionDeclaration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(matchedDomains) == 0 && fallbackToAll {
		return r.allDeclarationsLocked()
	}

	selected := r.coreDeclarationsLocked()
	seen := make(map[string]bool)
	for _, d := range selected {
		seen[d.Name] = true
	}

	domainSet := make(map[DomainGroup]bool)
	for _, dg := range matchedDomains {
		domainSet[dg] = true
	}

	// If no domain matched and fallbackToAll is false, add default ambient domains (e.g. Calendar & Tasks)
	if len(matchedDomains) == 0 {
		domainSet[DomainCalendar] = true
		domainSet[DomainTasks] = true
	}

	for _, m := range r.modules {
		modDomain, hasDomain := ModuleDomainMapping[m.Name()]
		if !hasDomain || !domainSet[modDomain] {
			continue
		}

		for _, d := range m.Declarations() {
			if d != nil && d.Name != "" && !seen[d.Name] {
				seen[d.Name] = true
				selected = append(selected, d)
			}
		}
	}

	return selected
}

func getCurrentTimeDeclaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        "get_current_time",
		Description: "Returns the current date, time, and day of the week in the user's local timezone.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"timezone": {
					Type:        genai.TypeString,
					Description: "Optional IANA timezone (e.g. 'Asia/Jerusalem', 'America/New_York').",
				},
			},
		},
	}
}
