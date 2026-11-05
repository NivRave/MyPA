package orchestrator

import (
	"context"
	"strings"
	"sync"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
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

	// Execute invokes the specified tool within this module.
	Execute(ctx context.Context, callCtx CallContext, toolName string, args map[string]interface{}) (string, error)

	// SystemPromptSection returns dynamic system instructions for this module,
	// such as baby age, active shared spaces, living profiles, or current budgets.
	SystemPromptSection(ctx context.Context, callCtx CallContext) string
}

// Registry manages the set of registered ToolModules and dispatches tool execution.
type Registry struct {
	mu      sync.RWMutex
	modules []ToolModule
	byName  map[string]ToolModule
	byTool  map[string]ToolModule
}

// NewRegistry initializes an empty ToolModule Registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make([]ToolModule, 0),
		byName:  make(map[string]ToolModule),
		byTool:  make(map[string]ToolModule),
	}
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
// SystemPromptSection outputs into a single formatted string.
func (r *Registry) CombinedSystemPrompt(ctx context.Context, callCtx CallContext) string {
	r.mu.RLock()
	mods := make([]ToolModule, len(r.modules))
	copy(mods, r.modules)
	r.mu.RUnlock()

	var sections []string
	for _, m := range mods {
		sec := strings.TrimSpace(m.SystemPromptSection(ctx, callCtx))
		if sec != "" {
			sections = append(sections, sec)
		}
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}
