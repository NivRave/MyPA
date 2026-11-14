package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChallenger1_Scale01_CacheHitMissAndTTL verifies:
// 1. After a cache miss, Redis stores ctx_prompt:<familyGroup>:<module> with ~10-minute TTL.
// 2. Subsequent calls return cached content without invoking module SystemPromptSection.
// 3. Partial cache hits only invoke missing modules and re-populate the cache.
// 4. Fallback to ActorID works when FamilyGroup is absent.
// 5. Empty prompt sections are cached properly to prevent repetitive empty DB queries.
func TestChallenger1_Scale01_CacheHitMissAndTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)

	var babyCalls int64
	var financeCalls int64
	var emptyCalls int64

	babyMod := &mockToolModule{
		name: "baby",
		promptFunc: func(ctx context.Context, c CallContext) string {
			atomic.AddInt64(&babyCalls, 1)
			return "Baby Section: 2 active profiles (Liam, Emma)"
		},
	}
	financeMod := &mockToolModule{
		name: "finance",
		promptFunc: func(ctx context.Context, c CallContext) string {
			atomic.AddInt64(&financeCalls, 1)
			return "Finance Section: Monthly Budget 85% consumed"
		},
	}
	emptyMod := &mockToolModule{
		name: "empty_domain",
		promptFunc: func(ctx context.Context, c CallContext) string {
			atomic.AddInt64(&emptyCalls, 1)
			return ""
		},
	}

	reg.Register(babyMod)
	reg.Register(financeMod)
	reg.Register(emptyMod)

	callCtx := CallContext{
		User: &models.User{
			FamilyGroup: "fam_stress_1",
			PlatformID:  "tg_parent_1",
		},
	}

	ctx := context.Background()

	// Step 1: Initial call - complete cache miss
	prompt1 := reg.CombinedSystemPrompt(ctx, callCtx)
	assert.Contains(t, prompt1, "Baby Section: 2 active profiles (Liam, Emma)")
	assert.Contains(t, prompt1, "Finance Section: Monthly Budget 85% consumed")
	assert.Equal(t, int64(1), atomic.LoadInt64(&babyCalls), "Baby module must be evaluated once on cache miss")
	assert.Equal(t, int64(1), atomic.LoadInt64(&financeCalls), "Finance module must be evaluated once on cache miss")
	assert.Equal(t, int64(1), atomic.LoadInt64(&emptyCalls), "Empty module must be evaluated once on cache miss")

	// Step 2: Verify keys and TTL in Redis
	babyKey := "ctx_prompt:fam_stress_1:baby"
	financeKey := "ctx_prompt:fam_stress_1:finance"
	emptyKey := "ctx_prompt:fam_stress_1:empty_domain"

	assert.True(t, mr.Exists(babyKey), "Redis must contain baby prompt cache key")
	assert.True(t, mr.Exists(financeKey), "Redis must contain finance prompt cache key")
	assert.True(t, mr.Exists(emptyKey), "Redis must contain empty_domain prompt cache key")

	babyTTL := mr.TTL(babyKey)
	financeTTL := mr.TTL(financeKey)
	assert.True(t, babyTTL > 9*time.Minute && babyTTL <= 10*time.Minute, "Baby key TTL must be near 10 minutes, got %v", babyTTL)
	assert.True(t, financeTTL > 9*time.Minute && financeTTL <= 10*time.Minute, "Finance key TTL must be near 10 minutes, got %v", financeTTL)

	// Step 3: Second call - complete cache hit
	prompt2 := reg.CombinedSystemPrompt(ctx, callCtx)
	assert.Equal(t, prompt1, prompt2)
	assert.Equal(t, int64(1), atomic.LoadInt64(&babyCalls), "Baby module must NOT be evaluated on cache hit")
	assert.Equal(t, int64(1), atomic.LoadInt64(&financeCalls), "Finance module must NOT be evaluated on cache hit")
	assert.Equal(t, int64(1), atomic.LoadInt64(&emptyCalls), "Empty module must NOT be evaluated on cache hit")

	// Step 4: Evict only baby key to simulate partial cache miss
	require.NoError(t, store.InvalidatePromptSections(ctx, babyKey))
	assert.False(t, mr.Exists(babyKey))

	prompt3 := reg.CombinedSystemPrompt(ctx, callCtx)
	assert.Equal(t, prompt1, prompt3)
	assert.Equal(t, int64(2), atomic.LoadInt64(&babyCalls), "Baby module must be re-evaluated after invalidation")
	assert.Equal(t, int64(1), atomic.LoadInt64(&financeCalls), "Finance module must remain cached")
	assert.Equal(t, int64(1), atomic.LoadInt64(&emptyCalls), "Empty module must remain cached")

	// Verify baby key was re-cached
	assert.True(t, mr.Exists(babyKey))

	// Step 5: Test ActorID fallback when FamilyGroup is empty
	callCtxNoFG := CallContext{
		User: &models.User{
			FamilyGroup: "",
			PlatformID:  "actor_alone_99",
		},
	}
	promptNoFG := reg.CombinedSystemPrompt(ctx, callCtxNoFG)
	assert.Contains(t, promptNoFG, "Baby Section")
	assert.True(t, mr.Exists("ctx_prompt:actor_alone_99:baby"), "Must cache using ActorID when FamilyGroup is empty")
}

// TestChallenger1_Scale01_EventBusInvalidationStress verifies:
// 1. Publishing MutationEvent topic "baby" evicts both ctx_prompt:<familyGroup>:baby and ctx_prompt:<actorUserID>:baby.
// 2. Unrelated tenant keys (e.g. fam_beta) are NOT evicted.
// 3. Unrelated module keys (e.g. finance, house) are NOT evicted.
// 4. Aliased topics (e.g. "calendar" -> "schedule" and "workspace_calendar") evict both mapped modules.
// 5. Entity-level fallbacks (e.g. entity_type: "topic_memory") evict "memory".
// 6. Topic case-insensitivity and whitespace trimming work robustly.
func TestChallenger1_Scale01_EventBusInvalidationStress(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, store)

	ctx := context.Background()

	// Seed keys across tenants and modules
	keysToSeed := map[string]string{
		"ctx_prompt:fam_alpha:baby":               "Baby fam_alpha",
		"ctx_prompt:user_john:baby":              "Baby user_john",
		"ctx_prompt:fam_beta:baby":                "Baby fam_beta", // Unrelated family
		"ctx_prompt:fam_alpha:finance":            "Finance fam_alpha", // Unrelated module
		"ctx_prompt:fam_alpha:schedule":           "Schedule fam_alpha",
		"ctx_prompt:fam_alpha:workspace_calendar": "Calendar fam_alpha",
		"ctx_prompt:fam_alpha:memory":             "Memory fam_alpha",
		"ctx_prompt:fam_alpha:house":              "House fam_alpha",
	}

	for k, v := range keysToSeed {
		mr.Set(k, v)
	}

	// 1. Publish MutationEvent for "baby"
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "  bAbY  ",
		FamilyGroup: "fam_alpha",
		ActorUserID: "user_john",
		Action:      "create",
	})
	require.NoError(t, err)

	// Assert target keys evicted
	assert.False(t, mr.Exists("ctx_prompt:fam_alpha:baby"), "fam_alpha:baby must be evicted")
	assert.False(t, mr.Exists("ctx_prompt:user_john:baby"), "user_john:baby must be evicted")

	// Assert unrelated tenant preserved
	assert.True(t, mr.Exists("ctx_prompt:fam_beta:baby"), "fam_beta:baby must NOT be evicted")

	// Assert unrelated module preserved
	assert.True(t, mr.Exists("ctx_prompt:fam_alpha:finance"), "fam_alpha:finance must NOT be evicted")

	// 2. Test topic alias "calendar" -> invalidates "schedule" and "workspace_calendar"
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "calendar",
		FamilyGroup: "fam_alpha",
		Action:      "update",
	})
	require.NoError(t, err)
	assert.False(t, mr.Exists("ctx_prompt:fam_alpha:schedule"), "schedule must be evicted by calendar topic")
	assert.False(t, mr.Exists("ctx_prompt:fam_alpha:workspace_calendar"), "workspace_calendar must be evicted by calendar topic")

	// 3. Test entity type fallback: Topic="health_custom", EntityType="topic_memory" -> invalidates "memory"
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "health_custom",
		EntityType:  "topic_memory",
		FamilyGroup: "fam_alpha",
		Action:      "upsert",
	})
	require.NoError(t, err)
	assert.False(t, mr.Exists("ctx_prompt:fam_alpha:memory"), "memory must be evicted when EntityType contains 'memory'")

	// House must still be untouched
	assert.True(t, mr.Exists("ctx_prompt:fam_alpha:house"), "house must still exist")
}

// TestChallenger1_Scale01_800msBoundedTimeoutStress verifies:
// 1. When slow/hanging modules sleep for 2+ seconds or block indefinitely,
//    CombinedSystemPrompt unblocks and returns within ~800ms-1100ms.
// 2. Prompt contains outputs from fast modules.
// 3. Prompt safely omits timed-out modules without panicking or stalling.
// 4. Timed-out modules are NOT cached to Redis.
func TestChallenger1_Scale01_800msBoundedTimeoutStress(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)

	fastMod1 := &mockToolModule{
		name: "fast_tasks",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Tasks: 3 pending"
		},
	}
	fastMod2 := &mockToolModule{
		name: "fast_weather",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Weather: 22C Sunny"
		},
	}
	slowSleepMod := &mockToolModule{
		name: "slow_sleeper",
		promptFunc: func(ctx context.Context, c CallContext) string {
			// Sleep 2.5 seconds (far exceeding 800ms)
			time.Sleep(2500 * time.Millisecond)
			return "Slow sleeper section should never appear"
		},
	}
	blockingHangMod := &mockToolModule{
		name: "hanging_mod",
		promptFunc: func(ctx context.Context, c CallContext) string {
			// Hang indefinitely until context cancellation
			<-ctx.Done()
			return "Hanging section should never appear"
		},
	}

	reg.Register(fastMod1)
	reg.Register(fastMod2)
	reg.Register(slowSleepMod)
	reg.Register(blockingHangMod)

	callCtx := CallContext{
		User: &models.User{FamilyGroup: "fam_timeout_test"},
	}

	start := time.Now()
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	elapsed := time.Since(start)

	// Bounded timeout assertions
	assert.True(t, elapsed >= 790*time.Millisecond, "Execution should wait until 800ms timeout, took %v", elapsed)
	assert.True(t, elapsed < 1200*time.Millisecond, "Execution must bound near 800ms (< 1200ms), took %v", elapsed)

	// Content assertions
	assert.Contains(t, prompt, "Tasks: 3 pending")
	assert.Contains(t, prompt, "Weather: 22C Sunny")
	assert.NotContains(t, prompt, "Slow sleeper section")
	assert.NotContains(t, prompt, "Hanging section")

	// Cache assertions: Fast modules cached; slow modules NOT cached
	assert.True(t, mr.Exists("ctx_prompt:fam_timeout_test:fast_tasks"))
	assert.True(t, mr.Exists("ctx_prompt:fam_timeout_test:fast_weather"))
	assert.False(t, mr.Exists("ctx_prompt:fam_timeout_test:slow_sleeper"), "Timed-out module must not be cached")
	assert.False(t, mr.Exists("ctx_prompt:fam_timeout_test:hanging_mod"), "Hanging module must not be cached")
}

// mockFailingPromptCache simulates various Redis failure scenarios.
type mockFailingPromptCache struct {
	getErr        error
	setErr        error
	invalidateErr error
	getHits       map[string]string
}

func (m *mockFailingPromptCache) GetPromptSection(ctx context.Context, familyGroup, module string) (string, bool, error) {
	if m.getErr != nil {
		return "", false, m.getErr
	}
	val, ok := m.getHits[module]
	return val, ok, nil
}

func (m *mockFailingPromptCache) GetPromptSections(ctx context.Context, familyGroup string, modules []string) (map[string]string, []string, error) {
	if m.getErr != nil {
		return nil, modules, m.getErr
	}
	hits := make(map[string]string)
	var misses []string
	for _, mod := range modules {
		if val, ok := m.getHits[mod]; ok {
			hits[mod] = val
		} else {
			misses = append(misses, mod)
		}
	}
	return hits, misses, nil
}

func (m *mockFailingPromptCache) SetPromptSection(ctx context.Context, familyGroup, module, content string, ttl time.Duration) error {
	if m.setErr != nil {
		return m.setErr
	}
	return nil
}

func (m *mockFailingPromptCache) InvalidatePromptSections(ctx context.Context, keys ...string) error {
	if m.invalidateErr != nil {
		return m.invalidateErr
	}
	return nil
}

// TestChallenger1_Scale01_RedisFailureDegradation verifies:
// 1. When Redis MGet fails with network errors, CombinedSystemPrompt degrades to live evaluation.
// 2. When Redis Set fails (e.g. OOM or read-only replica), CombinedSystemPrompt still returns cleanly.
// 3. When Redis Invalidate fails, EventBus does not crash or abort mutation publishing.
// 4. When miniredis is abruptly closed mid-operation, no panic occurs.
func TestChallenger1_Scale01_RedisFailureDegradation(t *testing.T) {
	callCtx := CallContext{
		User: &models.User{FamilyGroup: "fam_deg_1"},
	}

	liveMod := &mockToolModule{
		name: "live_eval",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Live evaluation content is active"
		},
	}

	// Case 1: Redis MGet and Set errors
	failingCache := &mockFailingPromptCache{
		getErr:        errors.New("dial tcp 127.0.0.1:6379: connect: connection refused"),
		setErr:        errors.New("OOM command not allowed when used memory > 'maxmemory'"),
		invalidateErr: errors.New("READONLY You can't write against a read only replica"),
	}

	reg := NewRegistry().WithPromptCache(failingCache)
	reg.Register(liveMod)

	// Must not panic, must execute live module successfully
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Equal(t, "Live evaluation content is active", prompt)

	// Case 2: Invalidation failure does not abort MutationEvent publication
	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, failingCache)
	err := bus.Publish(context.Background(), events.MutationEvent{
		Topic:       "baby",
		FamilyGroup: "fam_deg_1",
		Action:      "delete",
	})
	assert.NoError(t, err, "EventBus Publish must not fail when cache invalidation returns an error")

	// Case 3: miniredis abruptly closed
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)

	regClosed := NewRegistry().WithPromptCache(store)
	regClosed.Register(liveMod)

	// Close miniredis simulating network crash
	mr.Close()
	store.Close()

	// Should degrade gracefully to live evaluation
	promptAfterCrash := regClosed.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Equal(t, "Live evaluation content is active", promptAfterCrash)
}

// TestChallenger1_Scale01_ConcurrencyAndRaceStress executes intense concurrent
// prompt generations, cache invalidations, and module queries across 40 goroutines
// under `go test -race` to prove zero data races or deadlocks.
func TestChallenger1_Scale01_ConcurrencyAndRaceStress(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, store)

	reg := NewRegistry().WithPromptCache(store)

	domainNames := []string{"baby", "finance", "house", "memory", "schedule", "shared", "automation"}
	for _, d := range domainNames {
		dom := d
		reg.Register(&mockToolModule{
			name: dom,
			promptFunc: func(ctx context.Context, c CallContext) string {
				return fmt.Sprintf("Section for %s [%s]", dom, c.TenantFamilyGroup())
			},
		})
	}

	stopCh := make(chan struct{})
	time.AfterFunc(2*time.Second, func() {
		close(stopCh)
	})

	var wg sync.WaitGroup
	workers := 40
	families := []string{"fam_101", "fam_102", "fam_103", "fam_104"}

	// Launch reader goroutines calling CombinedSystemPrompt
	for i := 0; i < workers/2; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			fg := families[workerID%len(families)]
			callCtx := CallContext{
				User: &models.User{
					FamilyGroup: fg,
					PlatformID:  fmt.Sprintf("user_%d", workerID),
				},
			}
			for {
				select {
				case <-stopCh:
					return
				default:
					reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
					prompt := reg.CombinedSystemPrompt(reqCtx, callCtx)
					reqCancel()
					assert.NotEmpty(t, prompt)
				}
			}
		}()
	}

	// Launch invalidator goroutines publishing MutationEvents
	for i := 0; i < workers/4; i++ {
		workerID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			fg := families[workerID%len(families)]
			for {
				select {
				case <-stopCh:
					return
				default:
					topic := domainNames[workerID%len(domainNames)]
					_ = bus.Publish(context.Background(), events.MutationEvent{
						Topic:       topic,
						FamilyGroup: fg,
						ActorUserID: fmt.Sprintf("user_%d", workerID),
						Action:      "update",
					})
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// Launch reader goroutines selecting declarations
	for i := 0; i < workers/4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					decls := reg.SelectDeclarations([]DomainGroup{DomainBaby, DomainFinance}, false)
					assert.NotEmpty(t, decls)
					time.Sleep(3 * time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()
}
