package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/telegram/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestAdapter_RegistersWithItsDeclaredCapabilities(t *testing.T) {
	a := NewAdapter(Config{})
	require.NoError(t, channel.NewRegistry().Register(a))
	assert.Equal(t, "telegram", a.Name())

	caps := a.Capabilities()
	assert.Equal(t, 4000, caps.MaxTextLen, "today's split length (ADR 0003 §3)")
	assert.Equal(t, channel.HTML, caps.Markup)
	assert.Equal(t, []channel.AttachmentKind{channel.KindImage, channel.KindAudio}, caps.AttachmentsIn, "documents are BL-18")
	assert.Zero(t, caps.MaxChoices, "buttons are V4-10")
	assert.False(t, caps.Groups, "group chats are V4-16")
	assert.False(t, caps.VoiceOut, "voice replies are V4-18")
	assert.Empty(t, caps.AttachmentsOut)

	in := a.Ingress()
	require.NotNil(t, in.Webhook)
	assert.True(t, in.NeedsPublicAddress())
	require.Len(t, in.Webhook.Routes, 1)
	assert.Equal(t, "/webhook/telegram", in.Webhook.Routes[0].Path)
}

func TestAdapter_SendConvertsAndSplitsAsTheEngineDid(t *testing.T) {
	ctrl := gomock.NewController(t)
	api := mocks.NewMockAPI(ctrl)
	a := NewAdapter(Config{}, WithAPI(api))

	var chunks []string
	api.EXPECT().SendMessage(gomock.Any(), "555", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, text string) error {
		chunks = append(chunks, text)
		return nil
	}).AnyTimes()

	long := strings.Repeat("**word** ", 1000) // well over 4,000 bytes once converted
	rcpt, err := a.Send(context.Background(), channel.Address{Channel: Name, Conversation: "555", Person: "1"}, channel.Outbound{Text: long})
	require.NoError(t, err)
	assert.True(t, rcpt.Accepted)
	assert.Equal(t, SafeChunkTelegramHTML(ToTelegramHTML(long), 4000), chunks)
	for _, c := range chunks {
		assert.LessOrEqual(t, len(c), 4000)
	}
}

func TestAdapter_SendFallsBackToThePrivateChat(t *testing.T) {
	ctrl := gomock.NewController(t)
	api := mocks.NewMockAPI(ctrl)
	a := NewAdapter(Config{}, WithAPI(api))

	api.EXPECT().SendMessage(gomock.Any(), "7001", "<b>hi</b>").Return(nil)
	_, err := a.Send(context.Background(), channel.Address{Channel: Name, Person: "7001"}, channel.Outbound{Text: "**hi**"})
	require.NoError(t, err)

	_, err = a.Send(context.Background(), channel.Address{Channel: Name}, channel.Outbound{Text: "x"})
	assert.Error(t, err, "nowhere to send")

	api.EXPECT().SendMessage(gomock.Any(), "7001", gomock.Any()).Return(errors.New("bad request"))
	_, err = a.Send(context.Background(), channel.Address{Channel: Name, Person: "7001"}, channel.Outbound{Text: "x"})
	assert.EqualError(t, err, "bad request")
}

func TestAdapter_FetchResolvesTheFileThenDownloadsIt(t *testing.T) {
	ctrl := gomock.NewController(t)
	api := mocks.NewMockAPI(ctrl)
	a := NewAdapter(Config{}, WithAPI(api))

	gomock.InOrder(
		api.EXPECT().GetFile(gomock.Any(), "file-1").Return("photos/1.jpg", nil),
		api.EXPECT().DownloadFile(gomock.Any(), "photos/1.jpg").Return([]byte("jpeg"), nil),
	)
	rc, err := a.Fetch(context.Background(), channel.AttachmentRef{ID: "file-1", Kind: channel.KindImage})
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	assert.Equal(t, "jpeg", string(data))

	api.EXPECT().GetFile(gomock.Any(), "gone").Return("", errors.New("not found"))
	_, err = a.Fetch(context.Background(), channel.AttachmentRef{ID: "gone"})
	assert.Error(t, err)
}

func TestAdapter_RecognizesTelegramIDsOnly(t *testing.T) {
	a := NewAdapter(Config{})
	for _, id := range []string{"7001", "-1001234", "0"} {
		assert.True(t, a.Recognizes(id), id)
	}
	for _, id := range []string{"", "-", "+12025550199", "whatsapp:+12025550199", "12a", "user-1"} {
		assert.False(t, a.Recognizes(id), id)
	}
}

// The adapter's webhook route keeps the ingress's answers (V4-01): fail closed on the secret
// token, 200 for an update with nothing for the assistant, 500 when the queue refuses.
func TestAdapter_WebhookRouteFailsClosedAndQueues(t *testing.T) {
	const secret = "route-secret_1"
	route := NewAdapter(Config{SecretToken: secret}).Ingress().Webhook.Routes[0]
	var queued []channel.Inbound
	sinkErr := error(nil)
	h := route.Handler(func(_ context.Context, in channel.Inbound) error {
		if sinkErr != nil {
			return sinkErr
		}
		queued = append(queued, in)
		return nil
	})
	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, WebhookPath, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", token)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	update := `{"update_id":1,"message":{"message_id":9,"from":{"id":42},"chat":{"id":42},"date":1,"text":"hi"}}`

	assert.Equal(t, http.StatusUnauthorized, post("", update))
	assert.Equal(t, http.StatusUnauthorized, post("wrong", update))
	assert.Empty(t, queued)

	assert.Equal(t, http.StatusOK, post(secret, update))
	require.Len(t, queued, 1)
	assert.Equal(t, channel.Inbound{Channel: Name, MessageID: "9", Sender: "42",
		Conversation: channel.Conversation{ID: "42", Kind: channel.Direct, Addressed: true}, Text: "hi", SentAt: queued[0].SentAt}, queued[0])

	assert.Equal(t, http.StatusOK, post(secret, `{"update_id":2}`), "nothing for the assistant")
	assert.Len(t, queued, 1)

	sinkErr = errors.New("queue down")
	assert.Equal(t, http.StatusInternalServerError, post(secret, update), "Telegram retries")

	unconfigured := NewAdapter(Config{}).Ingress().Webhook.Routes[0].Handler(func(context.Context, channel.Inbound) error { return nil })
	rr := httptest.NewRecorder()
	unconfigured.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, WebhookPath, strings.NewReader(update)))
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "no configured secret: every request is refused")
}
