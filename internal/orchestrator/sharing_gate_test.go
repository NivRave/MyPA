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

// BL-72 (the owner's decision): after a turn has read untrusted content, a call that widens who
// sees something is staged for CONFIRM and changes nothing until it is confirmed; a call that makes
// something more private runs. Sharing a memory (save_topic_memory) and an entity profile were gated
// before, as whole tools.
func TestSharingIsGatedInAnUntrustedTurn(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		args  map[string]interface{}
		gated bool
	}{
		{"a transaction made shared", "log_financial_transaction", map[string]interface{}{"action": "update", "transaction_id": 7.0, "is_shared": true}, true},
		{"a transaction made shared (text)", "log_financial_transaction", map[string]interface{}{"action": "update", "transaction_id": 7.0, "is_shared": "yes"}, true},
		{"a transaction made private", "log_financial_transaction", map[string]interface{}{"action": "update", "transaction_id": 7.0, "is_shared": false}, false},
		{"a transaction's amount", "log_financial_transaction", map[string]interface{}{"action": "update", "transaction_id": 7.0, "amount": 9.0}, false},
		{"a topic shared", "configure_shared_topic", map[string]interface{}{"topic": "health", "can_view": true}, true},
		{"a topic shared by default", "configure_shared_topic", map[string]interface{}{"topic": "health", "shared_with_user_id": "omer"}, true},
		{"a topic made private", "configure_shared_topic", map[string]interface{}{"topic": "health", "can_view": false}, false},
		{"a shared calendar event", "create_calendar_event", map[string]interface{}{"title": "Party", "is_shared": true}, true},
		{"a private calendar event", "create_calendar_event", map[string]interface{}{"title": "Dentist", "is_shared": false}, false},
		{"a calendar event, private by default", "create_calendar_event", map[string]interface{}{"title": "Dentist"}, false},
		// The tool shares an event of a topic shared by default (family, baby, house, finance) unless
		// is_shared is a yes/no false; text in its place is ignored by the tool, so by the gate too.
		{"a family event, shared by default", "create_calendar_event", map[string]interface{}{"title": "Party", "topic": "Family"}, true},
		{"a family event with is_shared as text", "create_calendar_event", map[string]interface{}{"title": "Party", "topic": "family", "is_shared": "false"}, true},
		{"a family event made private", "create_calendar_event", map[string]interface{}{"title": "Therapy", "topic": "family", "is_shared": false}, false},
		{"a work event", "create_calendar_event", map[string]interface{}{"title": "Standup", "topic": "work"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInjectionHarness(t, call("search_emails"),
				&llm.Response{ToolCalls: []*genai.FunctionCall{{Name: tc.tool, Args: tc.args}}})
			sender := "u-share-" + tc.tool
			require.NoError(t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
				MessageID: "share-" + tc.name, Sender: sender, Conversation: channel.Conversation{ID: "c"},
				Text: "check my email", Channel: "telegram",
			}))
			ran := atomic.LoadInt64(h.ran[tc.tool])
			if !tc.gated {
				assert.Equal(t, int64(1), ran, "making something more private, or not wider, runs")
				return
			}
			assert.Zero(t, ran, "nothing changes before CONFIRM")

			// The person confirms: it runs once, as staged.
			require.NoError(t, h.engine.ProcessMessage(context.Background(), channel.Inbound{
				MessageID: "share-confirm-" + tc.name, Sender: sender, Conversation: channel.Conversation{ID: "c"},
				Text: "CONFIRM " + tc.tool, Channel: "telegram",
			}))
			assert.Equal(t, int64(1), atomic.LoadInt64(h.ran[tc.tool]), "it runs once confirmed")
		})
	}
}
