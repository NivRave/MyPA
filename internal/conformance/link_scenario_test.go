package conformance

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var linkCodeInReply = regexp.MustCompile(`/link ([0-9A-Z]{8})`)

// linkAccounts is V4-32's "done when": one person uses this channel and another as the same user
// with the same data. They ask for a code here, type it from their account on the other channel,
// and confirm here. From then on a message on the other channel is the same person: the same
// user, household and person key (the key every module writes and reads data by), and the
// conversation they had here. The other account is never treated as someone new: before the
// confirmation it is nobody, and the model is not consulted for any step of the linking.
func linkAccounts(t *testing.T, e *env) {
	other := e.otherChannel()
	here := e.member(1, "family", "Levi")
	there := other.person(2) // the same human's account on the other channel, unknown so far
	ctx := context.Background()

	// The person works here: a tool runs as them.
	e.model.script(
		callTool("delete_task", map[string]interface{}{"task_id": "41"}),
		say("Done: the plumber task is gone."),
	)
	e.says(here, "Delete the plumber task, the plumber comes Tuesday instead")
	before := e.tools.executed("delete_task")
	require.Len(t, before, 1)

	// 1. A code on the linked account.
	modelCalls := len(e.model.requests())
	e.says(here, "/link")
	m := linkCodeInReply.FindStringSubmatch(e.lastText())
	require.Len(t, m, 2, "the reply carries the code: %q", e.lastText())
	code := m[1]
	e.redact = map[string]string{code: "<CODE>"}

	// 2. Typed on the other channel by an account that belongs to nobody: nothing is linked yet,
	// and the first account is asked.
	require.True(t, e.deliverOn(other, inbound{From: there, Text: "/link " + code}))
	_, err := e.db.UserForAccount(ctx, other.name, there.UserID)
	assert.Error(t, err, "a typed code alone links nothing")
	texts := e.texts()
	require.GreaterOrEqual(t, len(texts), 2)
	assert.Contains(t, texts[len(texts)-2], "CONFIRM LINK", "the first account is asked to confirm")
	assert.Contains(t, texts[len(texts)-1], "Almost done", "the new account is told where to confirm")

	// A bare CONFIRM on the first account does not link (the confirmation names its action).
	e.says(here, "CONFIRM")
	_, err = e.db.UserForAccount(ctx, other.name, there.UserID)
	assert.Error(t, err, "a bare CONFIRM is not the link confirmation")

	// 3. Confirmed on the first account.
	e.says(here, "CONFIRM LINK")
	assert.Equal(t, modelCalls, len(e.model.requests()), "linking never consults the model")

	// The two accounts are one user now.
	a, err := e.db.UserForAccount(ctx, e.ch.name, here.UserID)
	require.NoError(t, err)
	b, err := e.db.UserForAccount(ctx, other.name, there.UserID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, b.ID, "both accounts are the same person")

	// 4. On the other channel: the same user with the same data, and the same conversation.
	e.model.script(
		func(call modelCall) *llm.Response {
			var seen []string
			for _, h := range call.History {
				seen = append(seen, h.Content)
			}
			assert.Contains(t, strings.Join(seen, "\n"), "the plumber comes Tuesday",
				"the other channel continues the person's conversation")
			return &llm.Response{ToolCalls: callTool("delete_task", map[string]interface{}{"task_id": "42"})(call).ToolCalls}
		},
		say("Done: the next one is gone too."),
	)
	require.True(t, e.deliverOn(other, inbound{From: there, Text: "And delete the next plumber task"}))
	after := e.tools.executed("delete_task")
	require.Len(t, after, 2)
	assert.Equal(t, before[0].ActorID, after[1].ActorID, "the same person key: every module reads and writes the same data")
	assert.Equal(t, before[0].FamilyGroup, after[1].FamilyGroup, "the same household")

	e.golden("link_accounts")
}
