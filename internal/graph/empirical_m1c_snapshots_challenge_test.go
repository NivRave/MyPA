package graph_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmpirical_M1C_CompleteSharedTask_Snapshots verifies:
// 1. BeforeJSON captures the pre-completion state (IsCompleted == false).
// 2. AfterJSON captures the completed state (IsCompleted == true).
// 3. Completing a non-existent task returns error and emits NO event.
// 4. Cross-tenant attempt to complete another family's task returns error and emits NO event.
func TestEmpirical_M1C_CompleteSharedTask_Snapshots(t *testing.T) {
	client := getTestDBClient(t)
	if client == nil {
		t.Skip("Database required for full resolver test")
		return
	}

	bus := events.NewEventBus()

	var publishedEvents []events.MutationEvent
	bus.Subscribe(func(ctx context.Context, ev events.MutationEvent) error {
		publishedEvents = append(publishedEvents, ev)
		return nil
	})

	resolver := graph.NewResolver(graph.ResolverConfig{
		DB:       client,
		EventBus: bus,
	})

	familyA := "FamilyAlphaSnapshots"
	familyB := "FamilyBetaSnapshots"

	userA := graph.WithClaims(context.Background(), &graph.AuthClaims{
		UserID:      "usr-alice",
		Role:        "member",
		FamilyGroup: familyA,
	})
	userB := graph.WithClaims(context.Background(), &graph.AuthClaims{
		UserID:      "usr-bob",
		Role:        "member",
		FamilyGroup: familyB,
	})

	t.Run("SnapshotDurability_BeforeAndAfterJSON_Populated", func(t *testing.T) {
		publishedEvents = nil

		// Create shared task in FamilyAlpha
		task, err := resolver.Mutation().CreateSharedTask(userA, familyA, "groceries", "Organic milk", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, task)
		require.Len(t, publishedEvents, 1)

		publishedEvents = nil

		// Complete shared task
		completed, err := resolver.Mutation().CompleteSharedTask(userA, task.ID, "usr-alice")
		require.NoError(t, err)
		require.NotNil(t, completed)
		assert.True(t, completed.IsCompleted)

		require.Len(t, publishedEvents, 1, "Exactly 1 MutationEvent must be published on CompleteSharedTask")
		ev := publishedEvents[0]
		assert.Equal(t, "shared", ev.Topic)
		assert.Equal(t, "update", ev.Action)
		assert.Equal(t, "shared_task_reminder", ev.EntityType)
		assert.Equal(t, task.ID, ev.EntityID)
		assert.Equal(t, "usr-alice", ev.ActorUserID)
		assert.Equal(t, familyA, ev.FamilyGroup)

		// Validate BeforeJSON
		assert.NotEmpty(t, ev.BeforeJSON, "BeforeJSON must not be empty")
		var beforeMap map[string]interface{}
		err = json.Unmarshal([]byte(ev.BeforeJSON), &beforeMap)
		require.NoError(t, err, "BeforeJSON must be valid JSON")
		assert.Equal(t, false, beforeMap["is_completed"], "BeforeJSON must record is_completed == false")
		assert.Equal(t, "Organic milk", beforeMap["title"])

		// Validate AfterJSON
		assert.NotEmpty(t, ev.AfterJSON, "AfterJSON must not be empty")
		var afterMap map[string]interface{}
		err = json.Unmarshal([]byte(ev.AfterJSON), &afterMap)
		require.NoError(t, err, "AfterJSON must be valid JSON")
		assert.Equal(t, true, afterMap["is_completed"], "AfterJSON must record is_completed == true")
		assert.Equal(t, "usr-alice", afterMap["completed_by_user_id"], "AfterJSON must record completing user")
		assert.NotNil(t, afterMap["completed_at"], "AfterJSON must record completed_at timestamp")
	})

	t.Run("NonExistentTask_ReturnsError_NoEventPublished", func(t *testing.T) {
		publishedEvents = nil

		_, err := resolver.Mutation().CompleteSharedTask(userA, "999999", "usr-alice")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
		assert.Empty(t, publishedEvents, "No event should be published when task is not found")
	})

	t.Run("CrossTenant_CompletionAttempt_Rejected_NoEventPublished", func(t *testing.T) {
		publishedEvents = nil

		// Task created by FamilyAlpha
		task, err := resolver.Mutation().CreateSharedTask(userA, familyA, "chores", "Mow the lawn", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, task)

		publishedEvents = nil

		// User from FamilyBeta attempts to complete FamilyAlpha's task
		_, err = resolver.Mutation().CompleteSharedTask(userB, task.ID, "usr-bob")
		require.Error(t, err, "User from different family group must be rejected")
		assert.Contains(t, err.Error(), "not found")
		assert.Empty(t, publishedEvents, "No event should be published on unauthorized cross-tenant attempt")
	})
}
