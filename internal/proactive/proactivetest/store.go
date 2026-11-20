// Package proactivetest has an in-memory proactive.Store, for tests of the policy and of the
// engine that uses it. The Postgres store is db.Client.
package proactivetest

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/proactive"
)

// Store keeps delivery rows in memory.
type Store struct {
	mu   sync.Mutex
	rows map[string]models.ProactiveDelivery
	seq  []string // IDs in creation order
}

var _ proactive.Store = (*Store)(nil)

// NewStore returns an empty store.
func NewStore() *Store { return &Store{rows: map[string]models.ProactiveDelivery{}} }

// All returns every row, in creation order.
func (s *Store) All() []models.ProactiveDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]models.ProactiveDelivery, 0, len(s.seq))
	for _, id := range s.seq {
		if d, ok := s.rows[id]; ok {
			out = append(out, d)
		}
	}
	return out
}

func (s *Store) CreateDelivery(_ context.Context, d *models.ProactiveDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	s.rows[d.ID] = *d
	s.seq = append(s.seq, d.ID)
	return nil
}

func (s *Store) SaveDelivery(_ context.Context, d *models.ProactiveDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[d.ID] = *d
	return nil
}

func (s *Store) ClaimDelivery(_ context.Context, id string, from []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.rows[id]
	if !ok || !in(d.State, from) {
		return false, nil
	}
	d.State = models.DeliverySending
	d.UpdatedAt = time.Now()
	s.rows[id] = d
	return true, nil
}

func (s *Store) HeldDeliveries(_ context.Context, personID string) ([]models.ProactiveDelivery, error) {
	return s.filter(func(d models.ProactiveDelivery) bool {
		return d.PersonID == personID && d.State == models.DeliveryHeld
	}), nil
}

func (s *Store) DueDeliveries(_ context.Context, now time.Time) ([]models.ProactiveDelivery, error) {
	return s.filter(func(d models.ProactiveDelivery) bool {
		return d.State == models.DeliveryPending && (d.NextAttemptAt == nil || !d.NextAttemptAt.After(now))
	}), nil
}

func (s *Store) DeliveryByRef(_ context.Context, channelName, ref string) (*models.ProactiveDelivery, error) {
	found := s.filter(func(d models.ProactiveDelivery) bool {
		return d.Channel == channelName && in(ref, strings.Split(d.Refs, ","))
	})
	if len(found) == 0 {
		return nil, nil
	}
	return &found[0], nil
}

func (s *Store) ExpireDeliveries(_ context.Context, now time.Time) ([]models.ProactiveDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.ProactiveDelivery
	for _, id := range s.seq {
		d := s.rows[id]
		if in(d.State, []string{models.DeliveryHeld, models.DeliveryPending}) && !d.ExpiresAt.After(now) {
			d.State, d.UpdatedAt = models.DeliveryExpired, now
			s.rows[id] = d
			out = append(out, d)
		}
	}
	return out, nil
}

func (s *Store) UnstickDeliveries(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, d := range s.rows {
		if d.State == models.DeliverySending && d.UpdatedAt.Before(before) {
			d.State, d.NextAttemptAt = models.DeliveryPending, nil
			s.rows[id] = d
			n++
		}
	}
	return n, nil
}

func (s *Store) PurgeDeliveries(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, d := range s.rows {
		if in(d.State, proactive.Ended) && d.UpdatedAt.Before(before) {
			delete(s.rows, id)
			n++
		}
	}
	return n, nil
}

// filter returns the matching rows, oldest first.
func (s *Store) filter(keep func(models.ProactiveDelivery) bool) []models.ProactiveDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.ProactiveDelivery
	for _, id := range s.seq {
		if d, ok := s.rows[id]; ok && keep(d) {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func in(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
