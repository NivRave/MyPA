package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"gorm.io/gorm"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
)

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require here.

type Resolver struct {
	DB *db.Client // Retained for fallback compatibility
	// Households reads households for building scopes (V4-30); DB when nil.
	Households     identity.Store
	EventBus       *events.EventBus          // Publishes MutationEvents for all mutations
	Durability     durability.RollbackEngine // Handles rollback and /undo delegation
	BabyRepo       db.BabyRepository
	FinanceRepo    db.FinanceRepository
	HouseRepo      db.HouseRepository
	ScheduleRepo   db.ScheduleRepository
	SharedRepo     *db.SharedRepository
	MemoryRepo     db.MemoryV2Repository
	AutomationRepo db.AutomationRepository
}

// ResolverConfig holds dependencies for constructing a fully decoupled Resolver.
type ResolverConfig struct {
	DB             *db.Client
	Households     identity.Store
	EventBus       *events.EventBus
	Durability     durability.RollbackEngine
	BabyRepo       db.BabyRepository
	FinanceRepo    db.FinanceRepository
	HouseRepo      db.HouseRepository
	ScheduleRepo   db.ScheduleRepository
	SharedRepo     *db.SharedRepository
	MemoryRepo     db.MemoryV2Repository
	AutomationRepo db.AutomationRepository
}

// NewResolver constructs a Resolver instance with injected repository interfaces.
func NewResolver(cfg ResolverConfig) *Resolver {
	r := &Resolver{
		DB:             cfg.DB,
		Households:     cfg.Households,
		EventBus:       cfg.EventBus,
		Durability:     cfg.Durability,
		BabyRepo:       cfg.BabyRepo,
		FinanceRepo:    cfg.FinanceRepo,
		HouseRepo:      cfg.HouseRepo,
		ScheduleRepo:   cfg.ScheduleRepo,
		SharedRepo:     cfg.SharedRepo,
		MemoryRepo:     cfg.MemoryRepo,
		AutomationRepo: cfg.AutomationRepo,
	}

	if r.DB != nil {
		if r.BabyRepo == nil {
			r.BabyRepo = r.DB
		}
		if r.FinanceRepo == nil {
			r.FinanceRepo = r.DB
		}
		if r.HouseRepo == nil {
			r.HouseRepo = r.DB
		}
		if r.ScheduleRepo == nil {
			r.ScheduleRepo = r.DB
		}
		if r.SharedRepo == nil {
			r.SharedRepo = db.NewSharedRepository(r.DB.DB)
		}
		if r.MemoryRepo == nil {
			r.MemoryRepo = r.DB.MemoryV2()
		}
		if r.AutomationRepo == nil {
			r.AutomationRepo = r.DB
		}
	}
	return r
}

// households returns the store scopes are built from: the configured one, else the database.
func (r *Resolver) households() identity.Store {
	if r.Households != nil {
		return r.Households
	}
	if r.DB != nil {
		return r.DB
	}
	return nil
}

// Fallback helper accessors to preserve 100% backward compatibility
func (r *Resolver) babyRepo() db.BabyRepository {
	if r.BabyRepo != nil {
		return r.BabyRepo
	}
	return r.DB
}

func (r *Resolver) houseRepo() db.HouseRepository {
	if r.HouseRepo != nil {
		return r.HouseRepo
	}
	return r.DB
}

func (r *Resolver) financeRepo() db.FinanceRepository {
	if r.FinanceRepo != nil {
		return r.FinanceRepo
	}
	return r.DB
}

func (r *Resolver) scheduleRepo() db.ScheduleRepository {
	if r.ScheduleRepo != nil {
		return r.ScheduleRepo
	}
	return r.DB
}

func (r *Resolver) sharedRepo() *db.SharedRepository {
	if r.SharedRepo != nil {
		return r.SharedRepo
	}
	if r.DB != nil {
		return db.NewSharedRepository(r.DB.DB)
	}
	return nil
}

func (r *Resolver) memoryRepo() db.MemoryV2Repository {
	if r.MemoryRepo != nil {
		return r.MemoryRepo
	}
	if r.DB != nil {
		return r.DB.MemoryV2()
	}
	return nil
}

func (r *Resolver) autoRepo() db.AutomationRepository {
	if r.AutomationRepo != nil {
		return r.AutomationRepo
	}
	return r.DB
}

func (r *Resolver) durabilityEngine() durability.RollbackEngine {
	if r.Durability != nil {
		return r.Durability
	}
	if r.DB != nil {
		return durability.NewModule(r.DB, r.EventBus)
	}
	return nil
}

// resolveTenantAndActor performs positive multi-tenant authorization and returns the caller's
// scope (V4-30, ADR 0002 §9). The familyGroup arguments stay in the schema until V4-35; each is
// resolved to a household by its label, ignoring case as before (a label that matches two
// households is refused), and must be the caller's own household, compared by ID. Every query
// then filters by the household ID in the scope.
func (r *Resolver) resolveTenantAndActor(ctx context.Context, requestedFamilyGroup *string) (actorID string, scope identity.Scope, err error) {
	claims := GetAuthClaims(ctx)
	if claims == nil {
		// P3-2: never fall back to the requested family group — unauthenticated callers must not
		// be able to pick a tenant. The auth middleware always sets claims on /graphql.
		return "", identity.Scope{}, fmt.Errorf("unauthorized: no authenticated claims")
	}
	st := r.households()
	if st == nil {
		return "", identity.Scope{}, fmt.Errorf("permission denied: household lookup unavailable")
	}

	actorID = claims.UserID
	if actorID == "" {
		actorID = "user"
	}

	// DE-2: a caller without a family group is its own tenant (its user ID), whose household is
	// keyed by that ID since V4-30, so the dashboard sees what the bot wrote. There is no shared
	// "default" tenant; a caller with neither ID nor group has no tenant at all.
	own := strings.TrimSpace(claims.FamilyGroup)
	if own == "" {
		own = strings.TrimSpace(claims.UserID)
	}
	if own == "" {
		return "", identity.Scope{}, fmt.Errorf("permission denied: caller has neither a family group nor a user id")
	}

	var reqFG string
	if requestedFamilyGroup != nil {
		reqFG = strings.TrimSpace(*requestedFamilyGroup)
	}
	if reqFG == "*" {
		// "*" is a read scope (wantsAllTenants), never a tenant to read from or write into.
		return "", identity.Scope{}, fmt.Errorf("permission denied: %q is not a family group", reqFG)
	}

	role := identity.ParseRole(claims.Role)
	ownScope, err := identity.ForLabel(ctx, st, own, actorID, role)
	// Only global admins may pick another household; tenant-bound admins (the dashboard secret,
	// P3-2) fall through to the same positive check as members.
	if claims.IsGlobalAdmin() {
		if reqFG == "" {
			if err != nil {
				return "", identity.Scope{}, fmt.Errorf("permission denied: %w", err)
			}
			return actorID, ownScope, nil
		}
		picked, perr := identity.ForLabel(ctx, st, reqFG, actorID, role)
		if perr != nil {
			return "", identity.Scope{}, fmt.Errorf("permission denied: %w", perr)
		}
		return actorID, picked, nil
	}
	if err != nil {
		return "", identity.Scope{}, fmt.Errorf("permission denied: %w", err)
	}

	// Positive authorization: the requested household must be the caller's own, by ID.
	if reqFG != "" {
		requested, rerr := identity.ForLabel(ctx, st, reqFG, actorID, role)
		if rerr != nil || requested.HouseholdID() != ownScope.HouseholdID() {
			return "", identity.Scope{}, fmt.Errorf("permission denied: unauthorized access to family group %q", reqFG)
		}
	}
	return actorID, ownScope, nil
}

// resolveActingScope returns the scope a mutation on a row addressed by ID runs in: a global
// admin acts in the row's own household (householdOf reads it install-wide); every other caller
// acts in their own household from resolveTenantAndActor, and the repositories then refuse a row
// of another household.
func (r *Resolver) resolveActingScope(ctx context.Context, householdOf func(*gorm.DB) (string, error)) (string, identity.Scope, error) {
	claims := GetAuthClaims(ctx)
	if claims == nil {
		return "", identity.Scope{}, fmt.Errorf("unauthorized: no authenticated claims")
	}
	if !claims.IsGlobalAdmin() {
		return r.resolveTenantAndActor(ctx, nil)
	}
	if r.DB == nil {
		return "", identity.Scope{}, fmt.Errorf("household lookup unavailable")
	}
	actorID := claims.UserID
	if actorID == "" {
		actorID = "user"
	}
	householdID, err := householdOf(r.DB.DB.WithContext(ctx))
	if err != nil {
		return "", identity.Scope{}, err
	}
	s, err := r.scopeOfRow(ctx, householdID, actorID)
	if err != nil {
		return "", identity.Scope{}, fmt.Errorf("permission denied: %w", err)
	}
	return actorID, s, nil
}

// automationRuleHousehold reads an automation rule's household install-wide (for global admins).
func automationRuleHousehold(uID uint, id string) func(*gorm.DB) (string, error) {
	return func(db *gorm.DB) (string, error) {
		var rule models.AutomationRule
		if err := db.Select("household_id").First(&rule, uID).Error; err != nil {
			return "", fmt.Errorf("automation rule %s not found", id)
		}
		return rule.HouseholdID, nil
	}
}

// scopeOfRow is the scope a global admin acts in on a row addressed by ID: the row's own stored
// household (V4-30). Only callers that checked IsGlobalAdmin use it; everyone else acts in the
// scope resolveTenantAndActor returns.
func (r *Resolver) scopeOfRow(ctx context.Context, householdID, actorID string) (identity.Scope, error) {
	st := r.households()
	if st == nil {
		return identity.Scope{}, fmt.Errorf("household lookup unavailable")
	}
	return identity.ForStoredRow(ctx, st, householdID, actorID)
}

// wantsAllTenants reports an explicit cross-tenant read: only a global admin, and only when it
// names no family group or asks for "*". Resolvers must use this — never a magic tenant name such
// as the former "default" — to drop the family filter (DE-2).
func wantsAllTenants(claims *AuthClaims, familyGroup *string) bool {
	if !claims.IsGlobalAdmin() {
		return false
	}
	return familyGroup == nil || strings.TrimSpace(*familyGroup) == "" || strings.TrimSpace(*familyGroup) == "*"
}

// householdOfUser returns a user's household ID (for tenant-bound admin checks, P3-2, V4-30).
func (r *Resolver) householdOfUser(userID string) (string, error) {
	if r.DB == nil {
		return "", fmt.Errorf("user lookup unavailable")
	}
	u, err := r.DB.GetUser(userID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", fmt.Errorf("user not found")
	}
	return u.HouseholdID, nil
}

// resolveTargetUser decides whose per-user data (e.g. preferences) the caller may act on (P3-2):
// members always act on themselves; global admins on anyone; tenant-bound admins (the dashboard)
// only on users of their own family group — otherwise an explicit permission error.
func (r *Resolver) resolveTargetUser(ctx context.Context, requestedUserID, actorID string, caller identity.Scope) (string, error) {
	claims := GetAuthClaims(ctx)
	if claims == nil {
		return "", fmt.Errorf("unauthorized: no authenticated claims")
	}
	requestedUserID = strings.TrimSpace(requestedUserID)
	if requestedUserID == "" || requestedUserID == actorID || !claims.IsAdmin() {
		return actorID, nil
	}
	if canActOnUser(claims, caller.HouseholdID(), requestedUserID, r.householdOfUser) {
		return requestedUserID, nil
	}
	return "", fmt.Errorf("permission denied: user %q is outside your family group", requestedUserID)
}

// publishEvent emits MutationEvent to EventBus
func (r *Resolver) publishEvent(ctx context.Context, ev events.MutationEvent) {
	if r.EventBus == nil {
		return
	}
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if err := r.EventBus.Publish(ctx, ev); err != nil {
		slog.Warn("graph: failed to publish mutation event", "error", err, "entity", ev.EntityType, "id", ev.EntityID)
	}
}

func toJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
