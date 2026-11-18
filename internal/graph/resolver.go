package graph

import (
	"context"
	"encoding/json"
	"fmt"
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
	DB             *db.Client                  // Retained for fallback compatibility
	EventBus       *events.EventBus            // Publishes MutationEvents for all mutations
	Durability     durability.RollbackEngine   // Handles rollback and /undo delegation
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

// resolveTenantAndActor performs positive multi-tenant authorization
func (r *Resolver) resolveTenantAndActor(ctx context.Context, requestedFamilyGroup *string) (actorID string, familyGroup string, err error) {
	claims := GetAuthClaims(ctx)
	if claims == nil {
		// P3-2: never fall back to the requested family group — unauthenticated callers must not
		// be able to pick a tenant. The auth middleware always sets claims on /graphql.
		return "", "", fmt.Errorf("unauthorized: no authenticated claims")
	}

	actorID = claims.UserID
	if actorID == "" {
		actorID = "user"
	}

	// DE-2: a caller without a family group is its own tenant (its user ID) — the same fallback as
	// the orchestrator's CallContext.TenantFamilyGroup, so the dashboard sees what the bot wrote.
	// There is no shared "default" tenant; a caller with neither ID nor group has no tenant at all.
	own := strings.TrimSpace(claims.FamilyGroup)
	if own == "" {
		own = strings.TrimSpace(claims.UserID)
	}
	if own == "" {
		return "", "", fmt.Errorf("permission denied: caller has neither a family group nor a user id")
	}

	var reqFG string
	if requestedFamilyGroup != nil {
		reqFG = strings.TrimSpace(*requestedFamilyGroup)
	}
	if reqFG == "*" {
		// "*" is a read scope (wantsAllTenants), never a tenant to read from or write into.
		return "", "", fmt.Errorf("permission denied: %q is not a family group", reqFG)
	}

	// Only global admins may pick another family group; tenant-bound admins (the dashboard
	// secret, P3-2) fall through to the same positive check as members.
	if claims.IsGlobalAdmin() {
		if reqFG != "" {
			return actorID, reqFG, nil
		}
		return actorID, own, nil
	}

	// Non-admin positive authorization check
	if reqFG != "" && !strings.EqualFold(reqFG, own) {
		return "", "", fmt.Errorf("permission denied: unauthorized access to family group %q", reqFG)
	}
	return actorID, own, nil
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

// familyOfUser returns a user's family group (for tenant-bound admin checks, P3-2).
func (r *Resolver) familyOfUser(userID string) (string, error) {
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
	return u.FamilyGroup, nil
}

// resolveTargetUser decides whose per-user data (e.g. preferences) the caller may act on (P3-2):
// members always act on themselves; global admins on anyone; tenant-bound admins (the dashboard)
// only on users of their own family group — otherwise an explicit permission error.
func (r *Resolver) resolveTargetUser(ctx context.Context, requestedUserID, actorID string) (string, error) {
	claims := GetAuthClaims(ctx)
	if claims == nil {
		return "", fmt.Errorf("unauthorized: no authenticated claims")
	}
	requestedUserID = strings.TrimSpace(requestedUserID)
	if requestedUserID == "" || requestedUserID == actorID || !claims.IsAdmin() {
		return actorID, nil
	}
	if canActOnUser(claims, requestedUserID, r.familyOfUser) {
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
