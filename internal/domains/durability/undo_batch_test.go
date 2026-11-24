package durability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Undo after V4-07: whose change /undo takes (BL-65), entries that cannot be undone (BL-47), and
// a restored memory's embedding (BL-25, BL-57).

// logChange records a mutation of the scope's household as the durability module would.
func logChange(t *testing.T, c *db.Client, s identity.Scope, userID, action, entityType, entityID, before string, at time.Time) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, c.InsertMutationLog(s, models.MutationLog{ID: id, UserID: userID, Topic: "test", Action: action,
		EntityType: entityType, EntityID: entityID, BeforeJSON: before, Summary: action + " " + entityType, CreatedAt: at}))
	return id
}

// sharedRow is a row of the household's shared data (a baby care log).
func sharedRow(t *testing.T, c *db.Client, s identity.Scope, by string) string {
	t.Helper()
	row := models.BabyCareLog{BabyID: 1, HouseholdID: s.HouseholdID(), FamilyGroup: s.LegacyLabel(), LogType: "feeding",
		Timestamp: time.Now(), LoggedByUserID: by}
	require.NoError(t, c.DB.Create(&row).Error)
	return fmt.Sprint(row.ID)
}

// workflowRow is a person's own scheduled workflow (V4-07: a cron rule whose action is a prompt).
func workflowRow(t *testing.T, c *db.Client, s identity.Scope, person string) string {
	t.Helper()
	rule := &models.AutomationRule{UserID: person, Name: "Morning summary " + person, TriggerType: "cron", TriggerTopic: "*",
		ConditionJSON: `{"cron": "0 7 * * *"}`, ActionInstruction: "prompt: Summarise my day", IsActive: true}
	require.NoError(t, c.CreateAutomationRule(s, rule))
	return fmt.Sprint(rule.ID)
}

func rowExists(t *testing.T, c *db.Client, table, id string) bool {
	t.Helper()
	var n int64
	require.NoError(t, c.DB.Table(table).Where("id = ?", id).Count(&n).Error)
	return n == 1
}

func isUndone(t *testing.T, c *db.Client, s identity.Scope, id string) bool {
	t.Helper()
	l, err := c.GetMutationLogByID(s, id)
	require.NoError(t, err)
	return l.IsUndone
}

// BL-65: /undo takes the caller's own newest change. Member A's undo leaves member B's newer
// workflow in place and undoes A's own last change instead; B can undo their own, and A cannot
// undo B's by naming it.
func TestUndo_ChatUndoesTheCallersOwnNewestChange(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	a := householdtest.Scope(t, client, "undo_own", "member_a", "family")
	b := householdtest.Scope(t, client, "undo_own", "member_b", "family")
	require.Equal(t, a.HouseholdID(), b.HouseholdID())

	now := time.Now()
	feeding := sharedRow(t, client, a, "member_a")
	aLog := logChange(t, client, a, "member_a", "create", "BabyCareLog", feeding, "", now.Add(-time.Minute))
	workflow := workflowRow(t, client, b, "member_b")
	bLog := logChange(t, client, b, "member_b", "create", "AutomationRule", workflow, "", now)

	res, err := module.RollbackLast(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, aLog, res.MutationID, "A's /undo undoes A's own last change")
	assert.False(t, rowExists(t, client, "baby_care_logs", feeding))
	assert.True(t, rowExists(t, client, "automation_rules", workflow), "B's new workflow stays")
	assert.False(t, isUndone(t, client, b, bLog))

	_, err = module.Rollback(ctx, a, bLog)
	require.Error(t, err, "A cannot undo B's change in chat by naming it")
	assert.Contains(t, err.Error(), "permission denied")
	assert.True(t, rowExists(t, client, "automation_rules", workflow))

	res, err = module.RollbackLast(ctx, b)
	require.NoError(t, err)
	assert.Equal(t, bLog, res.MutationID, "B undoes their own workflow")
	assert.False(t, rowExists(t, client, "automation_rules", workflow))
}

// BL-47: an entry whose entity type cannot be undone (here a household settings change written
// before this fix) never blocks /undo: it takes the newest change that can be undone.
func TestUndo_SkipsEntriesThatCannotBeUndone(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	a := householdtest.Scope(t, client, "undo_skip", "owner_a", "admin")

	now := time.Now()
	feeding := sharedRow(t, client, a, "owner_a")
	aLog := logChange(t, client, a, "owner_a", "create", "BabyCareLog", feeding, "", now.Add(-time.Minute))
	stray := logChange(t, client, a, "owner_a", "update", "Household", a.HouseholdID(), `{"name":"Old"}`, now)

	res, err := module.RollbackLast(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, aLog, res.MutationID)
	assert.False(t, rowExists(t, client, "baby_care_logs", feeding))
	assert.False(t, isUndone(t, client, a, stray), "the entry that cannot be undone is left as it is")
}

// BL-65: the rollback itself checks whose change it is and whose row it touches, whoever calls
// it: a caller of the repository that skipped the module's selection gets the same answer.
func TestUndo_TheRollbackItselfChecksTheOwner(t *testing.T) {
	client := newTestDBClient(t)
	ctx := context.Background()
	owner := householdtest.Scope(t, client, "undo_fence", "owner_o", "admin")
	adultB := householdtest.Scope(t, client, "undo_fence", "adult_b", "family")
	adultC := householdtest.Scope(t, client, "undo_fence", "adult_c", "family")
	now := time.Now()

	// B's workflow is B's own: an owner cannot roll it back, from the dashboard or in chat.
	workflow := workflowRow(t, client, adultB, "adult_b")
	wfLog := logChange(t, client, adultB, "adult_b", "create", "AutomationRule", workflow, "", now)
	l, err := client.GetMutationLogByID(owner, wfLog)
	require.NoError(t, err)
	for _, via := range []models.UndoVia{models.UndoViaDashboard, models.UndoViaChat} {
		err := client.RollbackSnapshot(ctx, owner, *l, "automation_rules", via)
		require.Error(t, err, via)
		assert.Contains(t, err.Error(), "permission denied")
	}
	assert.True(t, rowExists(t, client, "automation_rules", workflow))

	// B's change to shared data: an owner may roll it back from the dashboard, not in chat; another
	// adult may not at all.
	feeding := sharedRow(t, client, adultB, "adult_b")
	shLog := logChange(t, client, adultB, "adult_b", "create", "BabyCareLog", feeding, "", now)
	l, err = client.GetMutationLogByID(owner, shLog)
	require.NoError(t, err)
	require.Error(t, client.RollbackSnapshot(ctx, owner, *l, "baby_care_logs", models.UndoViaChat))
	require.Error(t, client.RollbackSnapshot(ctx, adultC, *l, "baby_care_logs", models.UndoViaDashboard))
	assert.True(t, rowExists(t, client, "baby_care_logs", feeding))
	require.NoError(t, client.RollbackSnapshot(ctx, owner, *l, "baby_care_logs", models.UndoViaDashboard))
	assert.False(t, rowExists(t, client, "baby_care_logs", feeding))

	// A log of C's own that names B's workflow cannot touch it: a person-owned row is rolled back by
	// its person only, whoever wrote the log.
	crafted := logChange(t, client, adultC, "adult_c", "create", "AutomationRule", workflow, "", now)
	l, err = client.GetMutationLogByID(adultC, crafted)
	require.NoError(t, err)
	err = client.RollbackSnapshot(ctx, adultC, *l, "automation_rules", models.UndoViaChat)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	assert.True(t, rowExists(t, client, "automation_rules", workflow))
	assert.False(t, isUndone(t, client, adultC, crafted))
}

// fakeEmbed is an embedding model: every text about chocolate gets one direction, every other text
// another. Two models use orthogonal directions, so a vector of one means nothing in the other.
func fakeEmbed(model string) db.EmbedBatchFunc {
	return func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, txt := range texts {
			v := make([]float32, models.EmbeddingDimensions)
			axis := 0
			if !strings.Contains(strings.ToLower(txt), "chocolate") {
				axis = 1
			}
			if model == "model-b" {
				axis += 2
			}
			v[axis] = 1
			out[i] = v
		}
		return out, nil
	}
}

func queryVector(t *testing.T, model, text string) pgvector.Vector {
	t.Helper()
	v, err := fakeEmbed(model)(context.Background(), []string{text})
	require.NoError(t, err)
	return pgvector.NewVector(v[0])
}

// BL-25, BL-57: a memory restored by undo after a re-embed into another model holds no vector of
// the old model; it is marked as needing one, `reembed --missing` fills it in the current model,
// and it is found by meaning again. The same holds for a snapshot written before the fix, which
// still carries the old vector.
func TestUndo_RestoredMemoryIsEmbeddedInTheCurrentModel(t *testing.T) {
	for _, oldSnapshot := range []bool{false, true} {
		name := "snapshot without a vector"
		if oldSnapshot {
			name = "old snapshot with a vector"
		}
		t.Run(name, func(t *testing.T) {
			client := newTestDBClient(t)
			bus := events.NewEventBus()
			module := durability.NewModule(client, bus)
			var undoEvents []events.MutationEvent
			bus.SubscribeSync(func(_ context.Context, ev events.MutationEvent) error {
				if ev.Action == "undo" {
					undoEvents = append(undoEvents, ev)
				}
				return nil
			})
			ctx := context.Background()
			s := householdtest.Scope(t, client, "undo_embed", "member_e", "family")
			repo := db.NewMemoryV2Repo(client.DB)

			for _, fact := range []string{"Likes dark chocolate 85%", "Car insurance renews in May"} {
				m := &models.TopicMemory{UserID: "member_e", Topic: models.TopicFamily, Subtopic: "facts", Fact: fact,
					Scope: "family", CreatedByUserID: "member_e"}
				require.NoError(t, repo.SaveTopicMemory(ctx, s, m))
			}
			_, err := client.Reembed(ctx, fakeEmbed("model-a"), db.ReembedOptions{Model: "model-a"})
			require.NoError(t, err)

			var choc models.TopicMemory
			require.NoError(t, client.DB.Where("household_id = ? AND fact LIKE ?", s.HouseholdID(), "%chocolate%").First(&choc).Error)
			require.NotZero(t, choc.Embedding.Slice()[0], "embedded in model A")

			// The snapshot as the delete publishes it.
			before, err := json.Marshal(choc)
			require.NoError(t, err)
			if oldSnapshot {
				var m map[string]interface{}
				require.NoError(t, json.Unmarshal(before, &m))
				m["embedding"] = choc.Embedding.Slice()
				before, err = json.Marshal(m)
				require.NoError(t, err)
			} else {
				assert.NotContains(t, string(before), `"embedding"`, "a snapshot carries no vector")
			}
			require.NoError(t, client.DB.Delete(&models.TopicMemory{}, choc.ID).Error)
			mutID := logChange(t, client, s, "member_e", "delete", "TopicMemory", fmt.Sprint(choc.ID), string(before), time.Now())

			_, err = client.Reembed(ctx, fakeEmbed("model-b"), db.ReembedOptions{Model: "model-b"})
			require.NoError(t, err)

			res, err := module.RollbackLast(ctx, s)
			require.NoError(t, err)
			require.Equal(t, mutID, res.MutationID)
			assert.NotContains(t, res.Detail, `"embedding"`, "the reply does not list the old vector")
			require.Len(t, undoEvents, 1)
			assert.NotContains(t, undoEvents[0].BeforeJSON+undoEvents[0].AfterJSON, `"embedding"`, "the undo's own event carries no vector")
			var undoLogs []models.MutationLog
			require.NoError(t, client.DB.Where("household_id = ? AND action = ?", s.HouseholdID(), "undo").Find(&undoLogs).Error)
			require.Len(t, undoLogs, 1, "the undo is logged")
			assert.NotContains(t, undoLogs[0].BeforeJSON+undoLogs[0].AfterJSON, `"embedding"`, "nor its log entry")
			assert.Contains(t, undoLogs[0].AfterJSON, "Likes dark chocolate", "the entry still holds the snapshot")

			var zero int64
			require.NoError(t, client.DB.Table("topic_memories").Where("id = ? AND vector_norm(embedding) = 0", choc.ID).Count(&zero).Error)
			assert.Equal(t, int64(1), zero, "the restored memory holds no vector of the old model: it is marked as needing one")

			missing, err := client.Reembed(ctx, fakeEmbed("model-b"), db.ReembedOptions{Model: "model-b", MissingOnly: true})
			require.NoError(t, err)
			assert.Equal(t, 1, missing.Embedded, "reembed --missing finds it")

			found, err := repo.SearchSimilarMemories(ctx, s, queryVector(t, "model-b", "chocolate"), 1)
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, choc.ID, found[0].ID, "it is found by meaning again")
		})
	}
}

// BL-57, an undone update: a snapshot's vector is never written back. When the undo leaves the
// text as it is (a pin), the memory keeps its current vector, in the current model; when it puts
// other text back, the memory is marked as needing an embedding.
func TestUndo_UpdatedMemoryNeverGetsTheSnapshotsVector(t *testing.T) {
	for _, textChanges := range []bool{false, true} {
		name := "same text keeps the current vector"
		if textChanges {
			name = "other text is marked as needing one"
		}
		t.Run(name, func(t *testing.T) {
			client := newTestDBClient(t)
			module := durability.NewModule(client, events.NewEventBus())
			ctx := context.Background()
			s := householdtest.Scope(t, client, "undo_update_embed", "member_u", "family")
			repo := db.NewMemoryV2Repo(client.DB)

			m := &models.TopicMemory{UserID: "member_u", Topic: models.TopicFamily, Subtopic: "facts", Fact: "Likes dark chocolate 85%",
				Scope: "family", CreatedByUserID: "member_u"}
			require.NoError(t, repo.SaveTopicMemory(ctx, s, m))
			_, err := client.Reembed(ctx, fakeEmbed("model-a"), db.ReembedOptions{Model: "model-a"})
			require.NoError(t, err)
			var cur models.TopicMemory
			require.NoError(t, client.DB.First(&cur, m.ID).Error)

			// An old-style snapshot (before BL-25), holding the model-A vector.
			before := map[string]interface{}{"is_pinned": false, "fact": cur.Fact, "embedding": cur.Embedding.Slice()}
			if textChanges {
				before["fact"] = "Likes milk chocolate"
			}
			raw, err := json.Marshal(before)
			require.NoError(t, err)
			require.NoError(t, client.DB.Model(&models.TopicMemory{}).Where("id = ?", m.ID).Update("is_pinned", true).Error)
			mutID := logChange(t, client, s, "member_u", "update", "topic_memory", fmt.Sprint(m.ID), string(raw), time.Now())

			_, err = client.Reembed(ctx, fakeEmbed("model-b"), db.ReembedOptions{Model: "model-b"})
			require.NoError(t, err)

			res, err := module.RollbackLast(ctx, s)
			require.NoError(t, err)
			require.Equal(t, mutID, res.MutationID)

			var got models.TopicMemory
			require.NoError(t, client.DB.First(&got, m.ID).Error)
			assert.False(t, got.IsPinned, "the update is undone")
			v := got.Embedding.Slice()
			assert.Zero(t, v[0], "no vector of the old model")
			if !textChanges {
				assert.Equal(t, float32(1), v[2], "the current model's vector is kept")
				return
			}
			assert.Equal(t, "Likes milk chocolate", got.Fact)
			assert.Zero(t, v[2], "marked as needing an embedding")
			missing, err := client.Reembed(ctx, fakeEmbed("model-b"), db.ReembedOptions{Model: "model-b", MissingOnly: true})
			require.NoError(t, err)
			assert.Equal(t, 1, missing.Embedded, "reembed --missing finds it")
		})
	}
}

// BL-65 on the update and delete paths: the rollback checks the row as it is and as it would be
// restored. An owner cannot restore a member's deleted private transaction or workflow, nor undo a
// member's update that made a transaction private or shared; the member can. A member's update to
// shared data an owner may undo from the dashboard.
func TestUndo_TheRollbackChecksTheOwnerOnUpdateAndDelete(t *testing.T) {
	client := newTestDBClient(t)
	ctx := context.Background()
	owner := householdtest.Scope(t, client, "undo_paths", "owner_p", "admin")
	b := householdtest.Scope(t, client, "undo_paths", "adult_p", "family")
	now := time.Now()

	newTx := func(shared bool) models.FinancialTransaction {
		tx := models.FinancialTransaction{UserID: "adult_p", HouseholdID: b.HouseholdID(), FamilyGroup: b.LegacyLabel(),
			Type: models.TransactionTypeExpense, Category: models.CategoryGroceries, Amount: 30, Currency: "USD",
			Merchant: "Shop", TransactionDate: now, IsShared: shared}
		require.NoError(t, client.DB.Create(&tx).Error)
		return tx
	}
	logOf := func(id string) models.MutationLog {
		l, err := client.GetMutationLogByID(owner, id)
		require.NoError(t, err)
		return *l
	}
	denied := func(err error, why string) {
		t.Helper()
		require.Error(t, err, why)
		assert.Contains(t, err.Error(), "permission denied", why)
	}

	t.Run("a deleted private transaction", func(t *testing.T) {
		tx := newTx(false)
		before, err := json.Marshal(tx)
		require.NoError(t, err)
		require.NoError(t, client.DB.Delete(&models.FinancialTransaction{}, tx.ID).Error)
		id := logChange(t, client, b, "adult_p", "delete", "FinancialTransaction", fmt.Sprint(tx.ID), string(before), now)
		denied(client.RollbackSnapshot(ctx, owner, logOf(id), "financial_transactions", models.UndoViaDashboard), "an owner may not restore it")
		assert.False(t, rowExists(t, client, "financial_transactions", fmt.Sprint(tx.ID)))
		require.NoError(t, client.RollbackSnapshot(ctx, b, logOf(id), "financial_transactions", models.UndoViaChat), "its person may")
		assert.True(t, rowExists(t, client, "financial_transactions", fmt.Sprint(tx.ID)))
	})

	t.Run("a deleted workflow", func(t *testing.T) {
		wf := workflowRow(t, client, b, "adult_p")
		var rule models.AutomationRule
		require.NoError(t, client.DB.First(&rule, wf).Error)
		before, err := json.Marshal(rule)
		require.NoError(t, err)
		require.NoError(t, client.DB.Delete(&models.AutomationRule{}, rule.ID).Error)
		id := logChange(t, client, b, "adult_p", "delete", "AutomationRule", wf, string(before), now)
		denied(client.RollbackSnapshot(ctx, owner, logOf(id), "automation_rules", models.UndoViaDashboard), "an owner may not restore it")
		assert.False(t, rowExists(t, client, "automation_rules", wf))
	})

	t.Run("an update that made a transaction private", func(t *testing.T) {
		tx := newTx(false)
		id := logChange(t, client, b, "adult_p", "update", "FinancialTransaction", fmt.Sprint(tx.ID), `{"is_shared": true}`, now)
		denied(client.RollbackSnapshot(ctx, owner, logOf(id), "financial_transactions", models.UndoViaDashboard), "the row is a person's own now")
		var got models.FinancialTransaction
		require.NoError(t, client.DB.First(&got, tx.ID).Error)
		assert.False(t, got.IsShared)
		require.NoError(t, client.RollbackSnapshot(ctx, b, logOf(id), "financial_transactions", models.UndoViaChat))
	})

	t.Run("an update that made a transaction shared", func(t *testing.T) {
		tx := newTx(true)
		id := logChange(t, client, b, "adult_p", "update", "FinancialTransaction", fmt.Sprint(tx.ID), `{"is_shared": false}`, now)
		denied(client.RollbackSnapshot(ctx, owner, logOf(id), "financial_transactions", models.UndoViaDashboard), "the restored row would be a person's own")
		var got models.FinancialTransaction
		require.NoError(t, client.DB.First(&got, tx.ID).Error)
		assert.True(t, got.IsShared)
	})

	t.Run("an update to shared data", func(t *testing.T) {
		feeding := sharedRow(t, client, b, "adult_p")
		id := logChange(t, client, b, "adult_p", "update", "BabyCareLog", feeding, `{"log_type": "diaper"}`, now)
		require.NoError(t, client.RollbackSnapshot(ctx, owner, logOf(id), "baby_care_logs", models.UndoViaDashboard), "an owner may")
		var got models.BabyCareLog
		require.NoError(t, client.DB.First(&got, feeding).Error)
		assert.Equal(t, "diaper", got.LogType)
	})
}
