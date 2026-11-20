package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
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
	// PromptCache drops cached prompt sections when the household's packs change (V4-27); nil
	// leaves them to expire.
	PromptCache PromptInvalidator
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
	PromptCache    PromptInvalidator
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
		PromptCache:    cfg.PromptCache,
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

// caller returns who a request acts as (V4-35): the signed-in person's key, as the modules write it
// into person columns (their platform ID, the same in chat), and their scope. The household is the
// person's own, read from their membership by the middleware; no argument can name another. Every
// dashboard field needs identity.UseDashboard: owners and adults, never children or guests.
func (r *Resolver) caller(ctx context.Context) (actorID string, scope identity.Scope, err error) {
	c, ok := callerOf(ctx)
	if !ok {
		return "", identity.Scope{}, fmt.Errorf("unauthorized: not signed in")
	}
	if !c.scope.Can(identity.UseDashboard) {
		return "", identity.Scope{}, fmt.Errorf("permission denied: the dashboard is for the household's owners and adults")
	}
	return c.scope.PersonKey(), c.scope, nil
}

// callerMay is caller for a field that needs an action beyond using the dashboard (an owner's).
func (r *Resolver) callerMay(ctx context.Context, a identity.Action) (string, identity.Scope, error) {
	actorID, s, err := r.caller(ctx)
	if err != nil {
		return "", identity.Scope{}, err
	}
	if !s.Can(a) {
		return "", identity.Scope{}, fmt.Errorf("permission denied: only an owner of the household may do this")
	}
	return actorID, s, nil
}

// ownedBy reports whether a row that belongs to one person is the caller's: by its person
// companion when the database filled it (V4-32), else by its old person key, the caller's
// platform ID or users.id.
func ownedBy(s identity.Scope, userID string, personID *string) bool {
	if personID != nil && strings.TrimSpace(*personID) != "" {
		return s.PersonID() != "" && strings.TrimSpace(*personID) == s.PersonID()
	}
	u := strings.TrimSpace(userID)
	return u != "" && (u == s.PersonKey() || u == s.PersonID())
}

// memoryVisible reports whether the caller may see a topic memory: the household's family
// memories, and the caller's own personal ones (as chat's recall reads them).
func memoryVisible(s identity.Scope, m *models.TopicMemory) bool {
	return strings.EqualFold(strings.TrimSpace(m.Scope), "family") || ownedBy(s, m.UserID, m.PersonID)
}

// ownRows is the SQL form of ownedBy for one column pair: the caller's rows by their person
// companion, or by the old person key where the companion is empty.
func ownRows(s identity.Scope, legacy, person string) (string, []interface{}) {
	if s.PersonID() == "" {
		return fmt.Sprintf("(%[2]s IS NULL AND %[1]s = ?)", legacy, person), []interface{}{s.PersonKey()}
	}
	return fmt.Sprintf("(%[2]s = ? OR (%[2]s IS NULL AND %[1]s IN (?, ?)))", legacy, person),
		[]interface{}{s.PersonID(), s.PersonKey(), s.PersonID()}
}

// member resolves a reference to another person (shared with, assigned to, completed by) inside
// the caller's household only, to the key the modules write (ADR 0002 Decision 3).
func (r *Resolver) member(ctx context.Context, s identity.Scope, ref string) (string, error) {
	if r.DB == nil {
		return "", fmt.Errorf("member lookup unavailable")
	}
	key, err := identity.Member(ctx, r.DB, s, ref)
	if err != nil {
		return "", fmt.Errorf("permission denied: %q is not a member of your household", strings.TrimSpace(ref))
	}
	return key, nil
}

// errNotYours is returned for a row of another member's personal data: the same answer as a row
// that does not exist, so nothing about it leaks.
func errNotYours(kind, id string) error { return fmt.Errorf("%s %s not found", kind, id) }

// resolveTargetUser decides whose per-person data (preferences, calendar settings) a field acts
// on: always the caller's own, whatever the request names (V4-35: an owner does not read or
// change another adult's personal data; members always got their own, P3-2). The result is the
// caller's person key, the key those rows are written under.
func (r *Resolver) resolveTargetUser(_ string, caller identity.Scope) (string, error) {
	if strings.TrimSpace(caller.PersonKey()) == "" {
		return "", fmt.Errorf("permission denied: no person")
	}
	return caller.PersonKey(), nil
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
