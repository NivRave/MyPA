package graph

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/nivik/mypa/internal/graph/model"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/packs"
)

// Feature packs in the API (V4-27). The dashboard hides a switched-off pack's pages; the API
// refuses its fields as well, so a page reached by its address shows nothing of the pack either.

// packFields maps every Query and Mutation field that belongs to a pack to that pack. Every other
// root field is Core (coreFields). TestPackGuard_EveryRootFieldIsClassified fails for a new root
// field that is in neither list.
var packFields = map[string]packs.ID{
	// Baby
	"babyProfiles": packs.Baby, "babyCareLogs": packs.Baby, "babyInventory": packs.Baby,
	"upsertBabyProfile": packs.Baby, "logBabyCare": packs.Baby, "updateBabyInventory": packs.Baby,
	// House and vehicles
	"houseContracts": packs.House, "homeMaintenanceItems": packs.House, "vehicleAssets": packs.House,
	"saveHouseContract": packs.House, "saveHomeMaintenanceItem": packs.House, "completeHomeMaintenance": packs.House,
	"saveVehicleAsset": packs.House,
	// Finance
	"financialTransactions": packs.Finance, "categoryBudgets": packs.Finance, "recurringBills": packs.Finance,
	"logFinancialTransaction": packs.Finance, "setCategoryBudget": packs.Finance, "saveRecurringBill": packs.Finance,
	// Automations
	"externalApiConnectors": packs.Automations, "automationRules": packs.Automations,
	"registerExternalApi": packs.Automations, "saveAutomationRule": packs.Automations,
	"toggleAutomationRule": packs.Automations, "deleteAutomationRule": packs.Automations,
}

// coreFields are the root fields that belong to no pack. Preferences and the personal CRM are
// Core (V4-27); Gmail has no fields.
var coreFields = map[string]bool{
	"me": true, "household": true, "users": true, "activePairingCodes": true, "joinSteps": true, "topicMemories": true,
	"memberAccounts": true, "removeMemberAccount": true, // BL-41
	"entityProfiles": true, "sharedSpaces": true, "sharedTasks": true, "scheduleEvents": true,
	"topicCalendarConfigs": true, "userPreference": true, "personalCrmContacts": true, "mutationLogs": true,
	"auditSessions": true, "userSessions": true, "usage": true, // usage: V4-04
	"signOut": true, "generatePairingCode": true, "updateHousehold": true, "setPackEnabled": true,
	"saveTopicMemory": true, "deleteTopicMemory": true, "pinTopicMemory": true, "upsertEntityProfile": true,
	"updateSharedSpace": true, "createSharedTask": true, "completeSharedTask": true, "createScheduleEvent": true,
	"deleteScheduleEvent": true, "saveTopicCalendarConfig": true, "saveUserPreference": true,
	"savePersonalCrmContact": true, "rollbackMutation": true,
}

// PackGuard refuses a root field of a pack the caller's household has switched off.
type PackGuard struct{}

var _ interface {
	graphql.HandlerExtension
	graphql.FieldInterceptor
} = PackGuard{}

// ExtensionName implements graphql.HandlerExtension.
func (PackGuard) ExtensionName() string { return "PackGuard" }

// Validate implements graphql.HandlerExtension.
func (PackGuard) Validate(graphql.ExecutableSchema) error { return nil }

// InterceptField implements graphql.FieldInterceptor.
func (PackGuard) InterceptField(ctx context.Context, next graphql.Resolver) (any, error) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || (fc.Object != "Query" && fc.Object != "Mutation") {
		return next(ctx)
	}
	id, ok := packFields[fc.Field.Name]
	if !ok {
		return next(ctx)
	}
	c, signedIn := callerOf(ctx)
	if !signedIn {
		return next(ctx) // the resolver refuses a caller who is not signed in
	}
	if !c.scope.Packs().Has(id) {
		p, _ := packs.Lookup(string(id))
		return nil, fmt.Errorf("pack switched off: the household has the %s pack off; an owner can switch it on under Tools & Packs", p.Name)
	}
	return next(ctx)
}

// NewServer is the app's GraphQL handler: the generated schema with the pack guard (V4-27).
func NewServer(r *Resolver) *handler.Server {
	srv := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: r}))
	srv.Use(PackGuard{})
	return srv
}

// topicSections are the Core modules whose prompt sections list the household's topics, which
// depend on its packs; they are dropped from the prompt cache when the packs change.
var topicSections = []string{"schedule", "shared"}

// setPackEnabled switches one pack of the scope's household. The household is never an argument.
// It is not a mutation-log entry: a household setting cannot be undone, and as the household's
// newest change it would block /undo of everything before it (the reason V4-35 keeps sign-ins off
// the log). The person who changed it and the result are logged at INFO.
func (r *Resolver) setPackEnabled(ctx context.Context, hs identity.Scope, id string, enabled bool) (*model.Household, error) {
	p, ok := packs.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("unknown pack %q", id)
	}
	if r.DB == nil {
		return nil, fmt.Errorf("household storage unavailable")
	}
	current, err := r.DB.HouseholdByID(ctx, hs.HouseholdID())
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("household not found")
	}
	next := packs.Parse(current.Packs).With(p.ID, enabled)
	after, err := r.DB.SetHouseholdPacks(ctx, hs, next)
	if err != nil {
		return nil, err
	}
	if after == nil {
		return nil, fmt.Errorf("household not found")
	}
	slog.Info("household pack changed", "household_id", hs.HouseholdID(), "person_id", hs.PersonID(),
		"pack", string(p.ID), "enabled", enabled, "packs", after.Packs)

	if r.PromptCache != nil {
		modules := append(append([]string(nil), topicSections...), p.Modules...)
		if _, err := r.PromptCache.InvalidateFamilyPromptSections(ctx, hs.HouseholdID(), modules); err != nil {
			slog.Warn("household pack changed; prompt cache not cleared, sections refresh within 10 minutes", "household_id", hs.HouseholdID(), "error", err)
		}
	}
	return toModelHousehold(*after), nil
}

// PromptInvalidator drops cached prompt sections (state.Store implements it).
type PromptInvalidator interface {
	InvalidateFamilyPromptSections(ctx context.Context, scope string, modules []string) (int, error)
}
