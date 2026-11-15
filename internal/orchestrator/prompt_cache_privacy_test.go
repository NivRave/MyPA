package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPrivacyStore(t *testing.T) (*miniredis.Miniredis, *state.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return mr, store
}

// personalModules mimic the real memory/automation modules: their sections depend on callCtx.User.
func personalModules() []ToolModule {
	return []ToolModule{
		&mockToolModule{name: "memory", promptFunc: func(_ context.Context, c CallContext) string {
			if c.ActorID() == "user_a" {
				return "Pinned memory: A's therapist appointment is on Tuesdays"
			}
			return "Pinned memory: (none)"
		}},
		&mockToolModule{name: "automation", promptFunc: func(_ context.Context, c CallContext) string {
			if c.ActorID() == "user_a" {
				return "Standing instruction: always reply to A in French"
			}
			return "Standing instruction: (none)"
		}},
	}
}

func familyCtx(userID, family string) CallContext {
	return CallContext{User: &models.User{PlatformID: userID, FamilyGroup: family}}
}

// P1-3 acceptance: user B in the same family must never see user A's personal sections,
// even with the cache warm from A's turn.
func TestPromptCache_PersonalSectionsDoNotLeakAcrossFamilyMembers(t *testing.T) {
	_, store := newPrivacyStore(t)
	reg := NewRegistry().WithPromptCache(store)
	for _, m := range personalModules() {
		reg.Register(m)
	}
	ctx := context.Background()

	promptA := reg.CombinedSystemPrompt(ctx, familyCtx("user_a", "fam_shared"))
	require.Contains(t, promptA, "therapist")
	require.Contains(t, promptA, "French")

	// Cache is now warm from A's turn. B (same family) builds a prompt.
	promptB := reg.CombinedSystemPrompt(ctx, familyCtx("user_b", "fam_shared"))
	assert.NotContains(t, promptB, "therapist", "B must not see A's pinned memory")
	assert.NotContains(t, promptB, "French", "B must not see A's standing instruction")

	// And A's cache entry still serves A (cache warm for both now).
	assert.Equal(t, promptA, reg.CombinedSystemPrompt(ctx, familyCtx("user_a", "fam_shared")))
}

func TestPromptCache_KeysIncludeUser(t *testing.T) {
	mr, store := newPrivacyStore(t)
	reg := NewRegistry().WithPromptCache(store)
	for _, m := range personalModules() {
		reg.Register(m)
	}
	_ = reg.CombinedSystemPrompt(context.Background(), familyCtx("user_a", "fam_shared"))

	assert.True(t, mr.Exists("ctx_prompt:fam_shared:user_a:memory"))
	assert.False(t, mr.Exists("ctx_prompt:fam_shared:memory"), "family-only keys must no longer be written")
}

func TestPromptCache_NoUserID_NoCaching(t *testing.T) {
	mr, store := newPrivacyStore(t)
	reg := NewRegistry().WithPromptCache(store)
	for _, m := range personalModules() {
		reg.Register(m)
	}
	// No PlatformID and no Message.UserID: identity unknown → must not cache under a shared key.
	_ = reg.CombinedSystemPrompt(context.Background(), CallContext{User: &models.User{FamilyGroup: "fam_x"}})
	assert.Empty(t, mr.Keys())
}

// Delimiter collision: family "a:b"+user "c" vs family "a"+user "b:c" must not share a key.
func TestPromptCache_ColonsInIdentifiersDoNotCollide(t *testing.T) {
	_, store := newPrivacyStore(t)
	ctx := context.Background()

	require.NoError(t, store.SetPromptSection(ctx, "a:b", "c", "memory", "secret of family a:b user c", time.Minute))
	_, found, err := store.GetPromptSection(ctx, "a", "b:c", "memory")
	require.NoError(t, err)
	assert.False(t, found, "different (family,user) pairs must never resolve to the same key")
}

// Invalidation: a family-scoped mutation evicts every member's cached section for that module,
// and nothing in other families or other modules.
func TestPromptCache_InvalidationEvictsAllFamilyMembers(t *testing.T) {
	mr, store := newPrivacyStore(t)
	ctx := context.Background()
	for _, u := range []string{"user_a", "user_b", "user_c"} {
		require.NoError(t, store.SetPromptSection(ctx, "fam_alpha", u, "memory", "m-"+u, time.Minute))
		require.NoError(t, store.SetPromptSection(ctx, "fam_alpha", u, "house", "h-"+u, time.Minute))
	}
	require.NoError(t, store.SetPromptSection(ctx, "fam_beta", "user_z", "memory", "m-z", time.Minute))

	bus := events.NewEventBus()
	RegisterPromptInvalidator(bus, store)
	require.NoError(t, bus.Publish(ctx, events.MutationEvent{Topic: "memory", FamilyGroup: "fam_alpha", ActorUserID: "user_a"}))

	for _, u := range []string{"user_a", "user_b", "user_c"} {
		assert.False(t, mr.Exists(fmt.Sprintf("ctx_prompt:fam_alpha:%s:memory", u)), "%s memory must be evicted", u)
		assert.True(t, mr.Exists(fmt.Sprintf("ctx_prompt:fam_alpha:%s:house", u)), "%s house must survive", u)
	}
	assert.True(t, mr.Exists("ctx_prompt:fam_beta:user_z:memory"), "other families must be untouched")
}

// Glob injection: a family literally named "*" must only invalidate its own keys.
func TestPromptCache_InvalidationEscapesGlobCharacters(t *testing.T) {
	mr, store := newPrivacyStore(t)
	ctx := context.Background()
	require.NoError(t, store.SetPromptSection(ctx, "*", "u1", "memory", "star family", time.Minute))
	require.NoError(t, store.SetPromptSection(ctx, "fam_real", "u2", "memory", "real family", time.Minute))

	n, err := store.InvalidateFamilyPromptSections(ctx, "*", []string{"memory"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.True(t, mr.Exists("ctx_prompt:fam_real:u2:memory"), "glob characters in the family name must be escaped")
}
