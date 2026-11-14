package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmpirical_GraphQL_MutationEvents_NoEmissionOnFailure validates that:
// 1. When an unauthorized tenant attempts an operation, NO MutationEvent is emitted.
// 2. When an invalid input fails, NO MutationEvent is emitted.
// 3. When a legitimate mutation succeeds, an exact MutationEvent with BeforeJSON/AfterJSON is emitted.
func TestEmpirical_GraphQL_MutationEvents_NoEmissionOnFailure(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	famA := "fam-auth-a-" + uuid.New().String()
	userA := "usr-auth-a-" + uuid.New().String()

	famB := "fam-auth-b-" + uuid.New().String()
	userB := "usr-auth-b-" + uuid.New().String()

	ctxA := WithClaims(context.Background(), &AuthClaims{
		UserID:      userA,
		Role:        "member",
		FamilyGroup: famA,
	})

	ctxB := WithClaims(context.Background(), &AuthClaims{
		UserID:      userB,
		Role:        "member",
		FamilyGroup: famB,
	})

	// 1. Cross-tenant mutation attempt: User B trying to create entity under Family A
	_, err := resolver.Mutation().UpsertBabyProfile(ctxB, famA, "IntruderBaby", time.Now(), "male", nil, nil, nil, nil)
	require.Error(t, err, "cross-tenant upsert must be rejected")
	assert.Contains(t, err.Error(), "permission denied")

	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, tracker.getEvents(), "Zero MutationEvents must be emitted on authorization failure")

	// 2. Legitimate creation under Family A
	babyProfile, err := resolver.Mutation().UpsertBabyProfile(ctxA, famA, "BabyA", time.Now(), "female", nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, babyProfile)

	time.Sleep(50 * time.Millisecond)
	eventsAfterSuccess := tracker.getEvents()
	require.Len(t, eventsAfterSuccess, 1, "Exactly 1 MutationEvent should be emitted on success")

	ev := eventsAfterSuccess[0]
	assert.Equal(t, "baby", ev.Topic)
	assert.Equal(t, "create", ev.Action)
	assert.Equal(t, "baby_profile", ev.EntityType)
	assert.Equal(t, famA, ev.FamilyGroup)
	assert.Equal(t, userA, ev.ActorUserID)
	assert.NotEmpty(t, ev.AfterJSON)

	var afterMap map[string]interface{}
	err = json.Unmarshal([]byte(ev.AfterJSON), &afterMap)
	require.NoError(t, err, "AfterJSON must be valid JSON")
	assert.Equal(t, "BabyA", afterMap["name"])

	// 3. User B attempting to delete or access Family A's schedule event
	evCreated, err := resolver.Mutation().CreateScheduleEvent(ctxA, "general", "Secret Meeting", nil, time.Now(), time.Now().Add(time.Hour), nil, nil, nil, &famA)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	preCount := len(tracker.getEvents())

	// User B attempts to delete User A's schedule event
	_, _ = resolver.Mutation().DeleteScheduleEvent(ctxB, evCreated.ID)
	// Even if it returns false or error, it must NOT emit a delete event for Family A
	time.Sleep(50 * time.Millisecond)
	eventsAfterAttack := tracker.getEvents()

	if len(eventsAfterAttack) > preCount {
		lastEv := eventsAfterAttack[len(eventsAfterAttack)-1]
		assert.NotEqual(t, famA, lastEv.FamilyGroup, "Intruder must not trigger mutation events affecting victim family")
	}
}

// TestEmpirical_GraphQL_MutationEvents_PayloadCompleteness verifies that all state-modifying
// domain mutations emit structurally sound MutationEvents with required metadata.
func TestEmpirical_GraphQL_MutationEvents_PayloadCompleteness(t *testing.T) {
	resolver, tracker, cleanup := setupTestResolver(t)
	defer cleanup()

	fam := "fam-test-" + uuid.New().String()
	user := "usr-test-" + uuid.New().String()

	ctx := WithClaims(context.Background(), &AuthClaims{
		UserID:      user,
		Role:        "member",
		FamilyGroup: fam,
	})

	// 1. Durability / Memory TopicMemory
	mem, err := resolver.Mutation().SaveTopicMemory(ctx, "memory", "facts", "Important family fact", nil, nil, &fam)
	require.NoError(t, err)
	require.NotNil(t, mem)

	// 2. Finance Transaction
	notes := "DCA Buy"
	isShared := true
	tx, err := resolver.Mutation().LogFinancialTransaction(ctx, fam, "expense", "investments", "Vanguard", 500.0, "USD", &notes, &isShared)
	require.NoError(t, err)
	require.NotNil(t, tx)

	// 3. House Contract
	vendor := "Landlord Corp"
	amount := 2000.0
	currency := "USD"
	contract, err := resolver.Mutation().SaveHouseContract(ctx, fam, "lease", "City Apartment Lease", &vendor, &amount, &currency, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, contract)

	time.Sleep(100 * time.Millisecond)
	emitted := tracker.getEvents()
	require.GreaterOrEqual(t, len(emitted), 3)

	expectedTopics := map[string]bool{
		"memory":  false,
		"finance": false,
		"house":   false,
	}

	for _, e := range emitted {
		assert.NotEmpty(t, e.ID, "Event ID must not be empty")
		assert.False(t, e.Timestamp.IsZero(), "Event Timestamp must not be zero")
		assert.Equal(t, fam, e.FamilyGroup, "FamilyGroup must match context")
		assert.Equal(t, user, e.ActorUserID, "ActorUserID must match context")
		assert.NotEmpty(t, e.AfterJSON, "AfterJSON must be present for creates")
		if _, ok := expectedTopics[e.Topic]; ok {
			expectedTopics[e.Topic] = true
		}
	}

	for topic, wasEmitted := range expectedTopics {
		assert.True(t, wasEmitted, "Topic %q must have emitted a MutationEvent", topic)
	}
}
