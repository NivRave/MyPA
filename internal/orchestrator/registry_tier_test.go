package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestRegistry_CombinedSystemPrompt_CacheHit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)
	callCtx := CallContext{
		User: &models.User{PlatformID: "user_1", FamilyGroup: "family_1"},
	}

	callCount := 0
	mod := &mockToolModule{
		name: "baby",
		promptFunc: func(ctx context.Context, c CallContext) string {
			callCount++
			return "Baby section: Active"
		},
	}
	reg.Register(mod)

	// Pre-seed Redis cache
	err = store.SetPromptSection(context.Background(), "family_1", "user_1", "baby", "Cached Baby Section", 10*time.Minute)
	require.NoError(t, err)

	// Execute CombinedSystemPrompt
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Equal(t, "Cached Baby Section", prompt)
	assert.Equal(t, 0, callCount, "Module promptFunc must not be called on cache hit")
}

func TestRegistry_CombinedSystemPrompt_CacheMissAndPopulate(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)
	callCtx := CallContext{
		User: &models.User{PlatformID: "user_2", FamilyGroup: "family_2"},
	}

	mod := &mockToolModule{
		name: "finance",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Finance section: Active"
		},
	}
	reg.Register(mod)

	// First execution: Cache miss -> populates Redis
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Equal(t, "Finance section: Active", prompt)

	// Verify key in Redis
	cached, found, err := store.GetPromptSection(context.Background(), "family_2", "user_2", "finance")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "Finance section: Active", cached)
}

func TestRegistry_CombinedSystemPrompt_Timeout(t *testing.T) {
	reg := NewRegistry() // without cache
	callCtx := CallContext{}

	fastMod := &mockToolModule{
		name: "fast",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Fast section"
		},
	}
	slowMod := &mockToolModule{
		name: "slow",
		promptFunc: func(ctx context.Context, c CallContext) string {
			time.Sleep(1200 * time.Millisecond) // exceeds 800ms
			return "Slow section"
		},
	}
	reg.Register(fastMod)
	reg.Register(slowMod)

	start := time.Now()
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	elapsed := time.Since(start)

	assert.Contains(t, prompt, "Fast section")
	assert.NotContains(t, prompt, "Slow section")
	assert.Less(t, elapsed, 1500*time.Millisecond, "Execution must bound near 800ms")
}

func TestEventBus_PromptInvalidation(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, store)

	ctx := context.Background()
	// Pre-seed cache
	_ = store.SetPromptSection(ctx, "fam1", "user1", "baby", "Baby section", 10*time.Minute)
	_ = store.SetPromptSection(ctx, "fam1", "user2", "baby", "Baby section", 10*time.Minute)

	// Publish mutation event
	err = bus.Publish(ctx, events.MutationEvent{
		Topic:       "baby",
		FamilyGroup: "fam1",
		ActorUserID: "user1",
		Action:      "create",
	})
	require.NoError(t, err)

	// Verify key is evicted
	_, found, err := store.GetPromptSection(ctx, "fam1", "user1", "baby")
	require.NoError(t, err)
	assert.False(t, found, "Key must be evicted after mutation event")
	_, found, err = store.GetPromptSection(ctx, "fam1", "user2", "baby")
	require.NoError(t, err)
	assert.False(t, found, "Other family members' keys must be evicted too")
}

func TestRegistry_TwoTierToolSelection(t *testing.T) {
	reg := NewRegistry()

	memMod := &mockToolModule{
		name: "memory",
		declarations: []*genai.FunctionDeclaration{
			{Name: "remember_fact"},
			{Name: "save_topic_memory"},
			{Name: "search_topic_memories"},
		},
	}
	babyMod := &mockToolModule{
		name: "baby",
		declarations: []*genai.FunctionDeclaration{
			{Name: "upsert_baby_profile"},
			{Name: "log_baby_care"},
			{Name: "manage_baby_inventory"},
		},
	}
	finMod := &mockToolModule{
		name: "finance",
		declarations: []*genai.FunctionDeclaration{
			{Name: "log_financial_transaction"},
			{Name: "get_financial_summary"},
		},
	}
	calMod := &mockToolModule{
		name: "workspace_calendar",
		declarations: []*genai.FunctionDeclaration{
			{Name: "create_calendar_event"},
			{Name: "list_calendar_events"},
		},
	}
	tasksMod := &mockToolModule{
		name: "workspace_tasks",
		declarations: []*genai.FunctionDeclaration{
			{Name: "list_tasks"},
			{Name: "create_task"},
		},
	}

	reg.Register(memMod)
	reg.Register(babyMod)
	reg.Register(finMod)
	reg.Register(calMod)
	reg.Register(tasksMod)

	// 1. CoreDeclarations: returns the 3 registered core tools + get_current_time = 4 tools
	core := reg.CoreDeclarations()
	coreNames := make(map[string]bool)
	for _, d := range core {
		coreNames[d.Name] = true
	}
	assert.True(t, coreNames["remember_fact"])
	assert.True(t, coreNames["save_topic_memory"])
	assert.True(t, coreNames["search_topic_memories"])
	assert.True(t, coreNames["get_current_time"])
	assert.False(t, coreNames["upsert_baby_profile"])

	// 2. Select Baby: Core + Baby
	babySelected := reg.SelectDeclarations([]DomainGroup{DomainBaby}, false)
	babyNames := make(map[string]bool)
	for _, d := range babySelected {
		babyNames[d.Name] = true
	}
	assert.True(t, babyNames["remember_fact"])
	assert.True(t, babyNames["upsert_baby_profile"])
	assert.True(t, babyNames["log_baby_care"])
	assert.False(t, babyNames["log_financial_transaction"])

	// 3. Select Multi: Baby + Finance
	multiSelected := reg.SelectDeclarations([]DomainGroup{DomainBaby, DomainFinance}, false)
	multiNames := make(map[string]bool)
	for _, d := range multiSelected {
		multiNames[d.Name] = true
	}
	assert.True(t, multiNames["upsert_baby_profile"])
	assert.True(t, multiNames["log_financial_transaction"])
	assert.False(t, multiNames["create_calendar_event"])

	// 4. Ambient Default: empty domains, fallback=false -> Core + Calendar + Tasks
	ambientSelected := reg.SelectDeclarations(nil, false)
	ambientNames := make(map[string]bool)
	for _, d := range ambientSelected {
		ambientNames[d.Name] = true
	}
	assert.True(t, ambientNames["remember_fact"])
	assert.True(t, ambientNames["create_calendar_event"])
	assert.True(t, ambientNames["list_tasks"])
	assert.False(t, ambientNames["upsert_baby_profile"])
	assert.False(t, ambientNames["log_financial_transaction"])

	// 5. FallbackToAll: empty domains, fallback=true -> all registered tools
	allSelected := reg.SelectDeclarations(nil, true)
	assert.Equal(t, len(reg.AllDeclarations()), len(allSelected))
}

func TestRegistry_CombinedSystemPrompt_PanicRecovery(t *testing.T) {
	reg := NewRegistry()
	callCtx := CallContext{
		User: &models.User{FamilyGroup: "family_panic"},
	}

	panickingMod := &mockToolModule{
		name: "panicker",
		promptFunc: func(ctx context.Context, c CallContext) string {
			panic("unexpected nil pointer in module logic")
		},
	}
	stableMod := &mockToolModule{
		name: "stable",
		promptFunc: func(ctx context.Context, c CallContext) string {
			return "Stable section active"
		},
	}
	reg.Register(panickingMod)
	reg.Register(stableMod)

	// CombinedSystemPrompt must recover from the panic and include the stable section
	prompt := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Contains(t, prompt, "Stable section active")
	assert.NotContains(t, prompt, "panic")
}

func TestRegistry_CombinedSystemPrompt_NegativeCaching(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	reg := NewRegistry().WithPromptCache(store)
	callCtx := CallContext{
		User: &models.User{FamilyGroup: "family_empty", PlatformID: "user_empty"},
	}

	callCount := 0
	emptyMod := &mockToolModule{
		name: "empty_domain",
		promptFunc: func(ctx context.Context, c CallContext) string {
			callCount++
			return "" // No records in domain
		},
	}
	reg.Register(emptyMod)

	// First call: executes module, caches empty string
	prompt1 := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Empty(t, prompt1)
	assert.Equal(t, 1, callCount)

	// Second call: hits negative cache, does NOT execute module
	prompt2 := reg.CombinedSystemPrompt(context.Background(), callCtx)
	assert.Empty(t, prompt2)
	assert.Equal(t, 1, callCount, "Negative cache hit must avoid re-executing module prompt function")
}

func TestRegistry_SelectDeclarations_NoNestedLockDeadlock(t *testing.T) {
	reg := NewRegistry()
	mod := &mockToolModule{
		name: "test_mod",
		declarations: []*genai.FunctionDeclaration{
			{Name: "test_tool"},
		},
	}
	reg.Register(mod)

	stop := make(chan struct{})
	done := make(chan struct{})

	// Concurrently invoke SelectDeclarations (readers)
	for i := 0; i < 5; i++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = reg.SelectDeclarations(nil, false)
					_ = reg.SelectDeclarations(nil, true)
				}
			}
		}()
	}

	// Concurrently invoke Register (writer acquiring Lock())
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			reg.Register(&mockToolModule{
				name: "dynamic_mod",
				declarations: []*genai.FunctionDeclaration{
					{Name: "dynamic_tool"},
				},
			})
		}
	}()

	select {
	case <-done:
		close(stop)
	case <-time.After(3 * time.Second):
		t.Fatal("Deadlock detected in SelectDeclarations with concurrent Register calls")
	}
}

