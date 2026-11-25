package durability_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/domains/durability"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-70 (the owner's decision): chat's change history and export follow the dashboard's rule for
// the mutation log. Each person sees their own entries in full; an owner also sees that another
// member's change of a type happened (its action and entity type), never its summary or snapshots;
// another adult sees none of another member's entries.
func TestChatHistory_AnotherMembersPrivateChangeIsNotShown(t *testing.T) {
	client := newTestDBClient(t)
	module := durability.NewModule(client, events.NewEventBus())
	ctx := context.Background()
	label := "history_" + uuid.NewString()[:8]
	omer := householdtest.Scope(t, client, label, "omer_h", "family")
	dana := householdtest.Scope(t, client, label, "dana_h", "family")
	owner := householdtest.Scope(t, client, label, "owner_h", "admin")

	entry := uuid.NewString()
	require.NoError(t, client.InsertMutationLog(omer, models.MutationLog{ID: entry, UserID: "omer_h", Topic: "finance",
		Action: "create", Kind: "transaction_logged", EntityType: "FinancialTransaction", EntityID: "41",
		AfterJSON: `{"id":41,"merchant":"Jeweller BL70","amount":950,"is_shared":false}`,
		Summary:   "Logged expense USD 950.00 for other (Jeweller BL70)", CreatedAt: time.Now()}))
	danaOwn := uuid.NewString()
	require.NoError(t, client.InsertMutationLog(dana, models.MutationLog{ID: danaOwn, UserID: "dana_h", Topic: "baby",
		Action: "create", EntityType: "BabyCareLog", EntityID: "5", Summary: "Logged feeding for Ari", CreatedAt: time.Now()}))

	read := func(s identity.Scope, tool string, args map[string]interface{}) string {
		t.Helper()
		out, err := module.Execute(ctx, orchestrator.CallContext{Scope: s}, tool, args)
		require.NoError(t, err)
		return out
	}
	outputs := func(s identity.Scope) map[string]string {
		return map[string]string{
			"history":     read(s, "list_recent_changes", map[string]interface{}{"topic": "all"}),
			"export json": read(s, "export_topic_data", map[string]interface{}{"topic": "all", "format": "json"}),
			"export csv":  read(s, "export_topic_data", map[string]interface{}{"topic": "all", "format": "csv"}),
		}
	}

	for name, out := range outputs(dana) {
		assert.NotContains(t, out, "Jeweller", "Dana's %s", name)
		assert.NotContains(t, out, "USD 950.00", "Dana's %s", name)
		assert.NotContains(t, out, entry, "Dana's %s does not list Omer's change at all", name)
		assert.Contains(t, out, "Logged feeding for Ari", "Dana's %s keeps her own change in full", name)
	}
	for name, out := range outputs(owner) {
		assert.NotContains(t, out, "Jeweller", "the owner's %s", name)
		assert.NotContains(t, out, "USD 950.00", "the owner's %s", name)
		assert.Contains(t, out, entry, "the owner's %s shows that the change happened", name)
		assert.Contains(t, out, "FinancialTransaction", "the owner's %s names its type", name)
		assert.NotContains(t, out, "Logged feeding for Ari", "nor Dana's summary to the owner (%s)", name)
	}
	for name, out := range outputs(omer) {
		assert.Contains(t, out, "Jeweller BL70", "Omer's %s keeps his own change in full", name)
	}
}
