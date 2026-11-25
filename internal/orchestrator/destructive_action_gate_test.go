package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// BL-66: deleting a transaction uses the confirmation rule of the other destructive tools. After
// a turn has read untrusted content, log_financial_transaction with action "delete" is staged for
// CONFIRM and does not run; logging and changing one are not destructive and run.
func TestDeletingATransactionIsGatedInAnUntrustedTurn(t *testing.T) {
	txCall := func(action string) *llm.Response {
		return &llm.Response{ToolCalls: []*genai.FunctionCall{{Name: "log_financial_transaction",
			Args: map[string]interface{}{"action": action, "transaction_id": 7.0, "amount": 5.0, "category": "other"}}}}
	}
	for _, tc := range []struct {
		action string
		gated  bool
	}{{"delete", true}, {"DELETE", true}, {"update", false}, {"log", false}, {"", false}} {
		t.Run("action "+tc.action, func(t *testing.T) {
			h := newInjectionHarness(t, call("search_emails"), txCall(tc.action))
			sender := "u-gate-" + tc.action
			require.NoError(t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
				MessageID: "gate-" + tc.action, Sender: sender, Conversation: channel.Conversation{ID: "c"},
				Text: "check my email", Channel: "telegram",
			}))
			ran := atomic.LoadInt64(h.ran["log_financial_transaction"])
			pending, err := h.engine.store.GetAndClearPendingMetaTool(context.Background(), sender)
			require.NoError(t, err)
			if tc.gated {
				assert.Zero(t, ran, "a delete after untrusted content must wait for CONFIRM")
				require.NotNil(t, pending, "and is staged")
				assert.Equal(t, "log_financial_transaction", pending.ToolName)
				return
			}
			assert.Equal(t, int64(1), ran, "%q is not destructive and runs", tc.action)
			assert.Nil(t, pending)
		})
	}

	// Without untrusted content a delete runs as asked.
	h := newInjectionHarness(t, txCall("delete"))
	require.NoError(t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
		MessageID: "gate-trusted", Sender: "u-gate-trusted", Conversation: channel.Conversation{ID: "c"},
		Text: "delete transaction 7", Channel: "telegram",
	}))
	assert.Equal(t, int64(1), atomic.LoadInt64(h.ran["log_financial_transaction"]))
}
