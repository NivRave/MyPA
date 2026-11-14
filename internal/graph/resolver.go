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
		fg := "default"
		if requestedFamilyGroup != nil && strings.TrimSpace(*requestedFamilyGroup) != "" {
			fg = strings.TrimSpace(*requestedFamilyGroup)
		}
		return "user", fg, nil
	}

	actorID = claims.UserID
	if actorID == "" {
		actorID = "user"
	}

	if claims.Role == "admin" {
		if requestedFamilyGroup != nil && strings.TrimSpace(*requestedFamilyGroup) != "" {
			familyGroup = strings.TrimSpace(*requestedFamilyGroup)
		} else if claims.FamilyGroup != "" {
			familyGroup = claims.FamilyGroup
		} else {
			familyGroup = "default"
		}
		return actorID, familyGroup, nil
	}

	// Non-admin positive authorization check
	if requestedFamilyGroup != nil && strings.TrimSpace(*requestedFamilyGroup) != "" {
		reqFG := strings.TrimSpace(*requestedFamilyGroup)
		if claims.FamilyGroup != "" && !strings.EqualFold(reqFG, claims.FamilyGroup) {
			return "", "", fmt.Errorf("permission denied: unauthorized access to family group %q", reqFG)
		}
	}

	if claims.FamilyGroup != "" {
		familyGroup = claims.FamilyGroup
	} else {
		familyGroup = "default"
	}

	return actorID, familyGroup, nil
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
