package memory_test

import (
	"context"
	"testing"

	"github.com/nivik/mypa/internal/domains/memory"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLLMForMemory struct {
	orchestrator.LLMClient
}

func (m *mockLLMForMemory) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 768), nil
}

type mockDBForMemory struct {
	orchestrator.DBClient
}

// TestChallenge2_Memory_AntiSpoofing verifies that memory tools (remember_fact, save_topic_memory,
// search_topic_memories, upsert_entity_profile) strictly use callCtx.ActorID() and callCtx.TenantFamilyGroup(),
// preventing caller-controlled argument overrides from cross-tenant spoofing.
func TestChallenge2_Memory_AntiSpoofing(t *testing.T) {
	repo := newInMemoryRepo()
	bus := events.NewEventBus()
	receivedEvents := make(chan events.MutationEvent, 20)
	bus.SubscribeTopic("memory", func(ctx context.Context, ev events.MutationEvent) error {
		receivedEvents <- ev
		return nil
	})

	mod := memory.NewModule(repo)

	legitUser := &models.User{
		ID:          "uuid-victim",
		PlatformID:  "mem_canonical_actor",
		FamilyGroup: "mem_legit_family",
	}
	dbMock := &mockDBForMemory{}
	llmMock := &mockLLMForMemory{}

	callCtx := orchestrator.CallContext{
		User:     legitUser,
		Message:  models.Message{UserID: "mem_canonical_actor"},
		DB:       dbMock,
		LLM:      llmMock,
		EventBus: bus,
	}

	adversarialArgs := map[string]interface{}{
		"topic":               "health",
		"fact":                "Allergic to penicillin",
		"scope":               "family",
		"entity_type":         "person",
		"entity_name":         "VictimPerson",
		"attributes":          map[string]interface{}{"blood_type": "O+"},
		"user_id":             "hacker_attacker_666",
		"family_group":        "evil_family_hacker",
		"created_by_user_id":  "hacker_attacker_666",
		"actor_user_id":       "hacker_attacker_666",
	}

	t.Run("remember_fact saves under canonical ActorID and TenantFamilyGroup", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "remember_fact", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, "saved to family memory successfully")

		// Verify V2 repo saved under canonical UserID and FamilyGroup
		memories, err := repo.SearchTopicMemories(context.Background(), "mem_canonical_actor", "mem_legit_family", "", "", nil)
		require.NoError(t, err)
		require.Len(t, memories, 1)
		assert.Equal(t, "mem_canonical_actor", memories[0].UserID)
		assert.Equal(t, "mem_legit_family", memories[0].FamilyGroup)

		// Verify evil family has no memories
		evilMemories, err := repo.SearchTopicMemories(context.Background(), "hacker_attacker_666", "evil_family_hacker", "", "", nil)
		require.NoError(t, err)
		assert.Empty(t, evilMemories)

		select {
		case ev := <-receivedEvents:
			assert.Equal(t, "mem_canonical_actor", ev.ActorUserID)
			assert.Equal(t, "mem_legit_family", ev.FamilyGroup)
		default:
			t.Fatal("Expected MutationEvent for remember_fact")
		}
	})

	t.Run("save_topic_memory ignores spoofed args", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "save_topic_memory", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, `"status":"saved"`)

		memories, err := repo.SearchTopicMemories(context.Background(), "mem_canonical_actor", "mem_legit_family", "health", "", nil)
		require.NoError(t, err)
		require.NotEmpty(t, memories)
		for _, m := range memories {
			assert.Equal(t, "mem_canonical_actor", m.UserID)
			assert.Equal(t, "mem_legit_family", m.FamilyGroup)
		}
	})

	t.Run("upsert_entity_profile scopes to TenantFamilyGroup", func(t *testing.T) {
		res, err := mod.Execute(context.Background(), callCtx, "upsert_entity_profile", adversarialArgs)
		require.NoError(t, err)
		assert.Contains(t, res, `"status":"upserted"`)

		// Verify profile belongs to legit family
		profile, err := repo.GetEntityProfile(context.Background(), "mem_legit_family", "health", "VictimPerson")
		require.NoError(t, err)
		require.NotNil(t, profile)
		assert.Equal(t, "mem_legit_family", profile.FamilyGroup)

		// Verify attacker cannot access it under evil_family
		evilProfile, _ := repo.GetEntityProfile(context.Background(), "evil_family_hacker", "health", "VictimPerson")
		assert.Nil(t, evilProfile)
	})
}
