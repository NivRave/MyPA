package automation

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"sync"
)

// Option configures an automation Module instance.
type Option func(*Module)

// WithRepository sets the database repository on the module.
func WithRepository(repo db.AutomationRepository) Option {
	return func(m *Module) {
		m.repo = repo
	}
}

// WithEventBus sets the event bus on the module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// WithRunner sets a custom runner on the module.
func WithRunner(r *Runner) Option {
	return func(m *Module) {
		m.runner = r
	}
}

// WithAllowLoopback sets loopback testing permission on the runner.
func WithAllowLoopback(allow bool) Option {
	return func(m *Module) {
		m.allowLoopback = allow
	}
}

// WithWorkflowsChanged sets the workflow change callback.
func WithWorkflowsChanged(fn func()) Option {
	return func(m *Module) {
		m.workflowsChanged = fn
	}
}

// Module implements orchestrator.ToolModule for on-demand external APIs, event-driven
// automation rules and scheduled workflows: the Automations pack (V4-27).
type Module struct {
	repo             db.AutomationRepository
	runner           *Runner
	bus              *events.EventBus
	workflowsChanged func()
	allowLoopback    bool
	mu               sync.RWMutex
}

// NewModule initializes a new automation Module.
func NewModule(repo db.AutomationRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}

	if m.runner == nil {
		var runnerOpts []RunnerOption
		if m.allowLoopback {
			runnerOpts = append(runnerOpts, WithAllowLoopbackForTesting(true))
		}
		m.runner = NewRunner(repo, m.bus, runnerOpts...)
	}

	return m
}

// Name returns the unique tool module name.
func (m *Module) Name() string {
	return "automation"
}

// SystemPromptSection describes the module's tools. A person's preferences, standing
// instructions and the personal CRM moved to the Core preferences module (V4-27), so switching
// the Automations pack off never drops them.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return "### Automations & External APIs:\n" +
		"• You manage on-demand external APIs (`register_external_api`, `call_external_api`), event-driven and scheduled automation rules (`create_automation_rule`), and scheduled workflows (`schedule_workflow`).\n"
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.AutomationRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.AutomationRepository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["automation_repo"].(db.AutomationRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("automation: no automation repository available in module or call context")
}
