package main

import (
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/automation"
	"github.com/nivik/mypa/internal/domains/baby"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/domains/finance"
	"github.com/nivik/mypa/internal/domains/house"
	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/domains/preferences"
	"github.com/nivik/mypa/internal/domains/schedule"
	"github.com/nivik/mypa/internal/domains/shared"
	"github.com/nivik/mypa/internal/domains/web_research"
	"github.com/nivik/mypa/internal/domains/workspace_contacts"
	"github.com/nivik/mypa/internal/domains/workspace_gmail"
	"github.com/nivik/mypa/internal/domains/workspace_tasks"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
)

// toolDeps are what the chat's tool modules are built from.
type toolDeps struct {
	db             *db.Client
	bus            *events.EventBus
	durability     *durability.Module
	sharedNotifier *shared.Notifier
	calendars      schedule.CalendarFactory
	runner         *automation.Runner
	gmail          orchestrator.GmailClient
	tasks          orchestrator.TasksClient
	contacts       orchestrator.ContactsClient
	web            orchestrator.TavilyClient
}

// toolModules are the chat's tool modules, in the order the worker registers them. The undo guard
// (undo_guard_test.go) walks the tools of exactly this list, so a tool added here without a proof
// that /undo reverses it, or a reason it cannot, fails the tests (BL-68).
func toolModules(d toolDeps) []orchestrator.ToolModule {
	return []orchestrator.ToolModule{
		workspace_gmail.NewModule(workspace_gmail.WithClient(d.gmail), workspace_gmail.WithEventBus(d.bus)),
		workspace_tasks.NewModule(workspace_tasks.WithClient(d.tasks), workspace_tasks.WithEventBus(d.bus)),
		workspace_contacts.NewModule(workspace_contacts.WithClient(d.contacts), workspace_contacts.WithEventBus(d.bus)),
		web_research.NewModule(web_research.WithClient(d.web)),

		// Track 1A: Durability Module (audit logs, undo, data export)
		d.durability,
		// Track 1B: Topic Memory Module (topic memory, entity profiles)
		memory.NewModule(db.NewMemoryV2Repo(d.db.DB)),
		// Track 1C: Shared Spaces & Partner Sync Notifier
		shared.NewModule(d.db, d.bus, d.sharedNotifier),
		// Track 1D: Multi-Schedule & Virtual Sub-Calendars
		schedule.NewModule(d.db, schedule.WithCalendarFactory(d.calendars), schedule.WithEventBus(d.bus)),
		// Track 2E: Baby & Family Care Tracker
		baby.NewModule(d.db, baby.WithEventBus(d.bus)),
		// Track 2F: House Maintenance, Contracts & Vehicle Vault
		house.NewModule(d.db, house.WithEventBus(d.bus)),
		// Track 2G: Financial Tracking & Budget Intelligence
		finance.NewModule(d.db, finance.WithEventBus(d.bus)),
		// Track 2H: On-Demand External APIs, Automations & Personal CRM
		automation.NewModule(d.db, automation.WithEventBus(d.bus), automation.WithRunner(d.runner)),
		// Core: a person's preferences and the personal CRM, outside every pack (V4-27)
		preferences.NewModule(d.db, preferences.WithEventBus(d.bus)),
	}
}
