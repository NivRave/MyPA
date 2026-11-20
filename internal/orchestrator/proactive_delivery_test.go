package orchestrator_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// recordingDelivery stands in for the delivery policy and records what the engine hands it.
type recordingDelivery struct {
	mu       sync.Mutex
	requests []proactive.Request
	fail     error
}

func (r *recordingDelivery) Deliver(_ context.Context, req proactive.Request) (*models.ProactiveDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return nil, r.fail
	}
	r.requests = append(r.requests, req)
	return &models.ProactiveDelivery{PersonID: "p", State: models.DeliveryHeld}, nil
}
func (r *recordingDelivery) Release(context.Context, string, string)              {}
func (r *recordingDelivery) Report(context.Context, string, channel.Report) error { return nil }
func (r *recordingDelivery) Sweep(context.Context)                                {}

func (r *recordingDelivery) got() []proactive.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]proactive.Request(nil), r.requests...)
}

// A due reminder is handed to the policy with its template (the title as one line) and when it
// was due, and is marked sent once handed over. Nothing is sent around the policy.
func TestDelivery_ReminderIsHandedToThePolicy(t *testing.T) {
	client := newTestDBClient(t)
	quietPerson(t, client, "deliver-rina", "DeliverRina", "UTC", "", "")
	due := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "deliver-rina", Message: "Call the dentist\nabout Noa", DueTime: due}))

	spy := &spyTelegramClient{}
	rec := &recordingDelivery{}
	engine := orchestrator.NewEngine(nil, nil, client, nil, adapterstest.Registry(spy, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithDelivery(rec)
	engine.CheckAndSendReminders()

	got := rec.got()
	require.Len(t, got, 1)
	assert.Equal(t, "deliver-rina", got[0].Key)
	assert.Equal(t, proactive.Reminder, got[0].Category)
	assert.Equal(t, channel.TemplateReminder, got[0].TemplateKey)
	assert.Equal(t, []string{"Call the dentist"}, got[0].TemplateValues)
	assert.Contains(t, got[0].Text, "Call the dentist\nabout Noa")
	assert.True(t, got[0].DueAt.Equal(due))
	assert.Empty(t, sentTexts(spy), "the policy sends, not the engine")

	pending, err := client.GetDueReminders()
	require.NoError(t, err)
	assert.Empty(t, pending, "handed over, so marked sent")
}

// A reminder the policy cannot take (the database is down) stays due for the next pass.
func TestDelivery_ReminderStaysDueWhenThePolicyFails(t *testing.T) {
	client := newTestDBClient(t)
	quietPerson(t, client, "deliver-omer", "DeliverOmer", "UTC", "", "")
	require.NoError(t, client.SaveReminder(models.ScheduledReminder{UserID: "deliver-omer", Message: "Water the plants", DueTime: time.Now().Add(-time.Minute)}))

	engine := orchestrator.NewEngine(nil, nil, client, nil, adapterstest.Registry(&spyTelegramClient{}, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithDelivery(&recordingDelivery{fail: errors.New("connection refused")})
	engine.CheckAndSendReminders()

	pending, err := client.GetDueReminders()
	require.NoError(t, err)
	assert.Len(t, pending, 1)
}

// The morning briefing's answer is a proactive message: it goes to the policy as a briefing that
// may go out as the briefing template (the date on the person's clock), expires at the end of
// their day, and only the turn's first answer may be the template.
func TestDelivery_BriefingAnswerIsHandedToThePolicy(t *testing.T) {
	client := newTestDBClient(t)
	mr := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ctrl := gomock.NewController(t)
	model := mocks.NewMockLLMClient(ctrl)
	model.EXPECT().GenerateEmbedding(gomock.Any(), gomock.Any()).Return(nil, errors.New("no embeddings in this test")).AnyTimes()
	model.EXPECT().Chat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&llm.Response{Text: "Here is your morning briefing."}, nil).AnyTimes()

	jerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)
	quietPerson(t, client, "deliver-dana", "DeliverDana", "Asia/Jerusalem", "", "")

	spy := &spyTelegramClient{}
	rec := &recordingDelivery{}
	at := time.Date(2026, 11, 20, 8, 0, 0, 0, jerusalem)
	engine := orchestrator.NewEngine(nil, store, client, model, adapterstest.Registry(spy, nil),
		nil, nil, nil, nil, nil, nil, nil, "UTC").WithQuietHours(client).WithDelivery(rec).
		WithClock(func() time.Time { return at })
	require.Equal(t, []string{"deliver-dana"}, engine.SendDueBriefings(context.Background()))
	engine.Wait()

	got := rec.got()
	require.NotEmpty(t, got)
	first := got[0]
	assert.Equal(t, "deliver-dana", first.Key)
	assert.Equal(t, proactive.Briefing, first.Category)
	assert.Equal(t, "Here is your morning briefing.", first.Text)
	assert.Equal(t, channel.TemplateBriefing, first.TemplateKey)
	assert.Equal(t, []string{"Friday, 20 November"}, first.TemplateValues)
	assert.True(t, first.ExpiresAt.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, jerusalem)), "the end of her day: %s", first.ExpiresAt)
	for _, later := range got[1:] {
		assert.Empty(t, later.TemplateKey, "one notice is enough")
	}
	assert.Empty(t, sentTexts(spy), "the answer goes through the policy, not straight to the chat")
}
