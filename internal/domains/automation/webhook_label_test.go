package automation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BL-68: a change is logged as the create/update/delete undo reads, with its own name in Kind. A
// webhook rule's receiver gets the event as before: the change's own name in "action", no "kind",
// and never the rows the same undo would put back (Also).
func TestWebhook_TheReceiverSeesTheChangeUnderItsOwnName(t *testing.T) {
	var mu sync.Mutex
	var body map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	runner := NewRunner(newFakeAutomationRepo(), nil, WithAllowLoopbackForTesting(true))
	rule := models.AutomationRule{Name: "Hook", ActionInstruction: "webhook: " + ts.URL, HouseholdID: "h1"}
	ev := events.MutationEvent{Topic: "finance", Action: "create", Kind: "transaction_logged", EntityType: "FinancialTransaction",
		EntityID: "7", HouseholdID: "h1", Summary: "Logged expense",
		Also: []models.UndoAlso{{EntityType: "TopicMemory", EntityID: "1", Before: map[string]interface{}{"fact": "secret"}}}}
	require.NoError(t, runner.ExecuteAction(context.Background(), rule, ev))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, body)
	assert.Equal(t, "transaction_logged", body["action"])
	assert.NotContains(t, body, "kind")
	assert.NotContains(t, body, "Also")
	assert.NotContains(t, body, "also")
}
