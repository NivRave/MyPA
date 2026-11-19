package twilio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/twilio/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestAdapter_RegistersWithItsDeclaredCapabilities(t *testing.T) {
	a := NewAdapter(Config{})
	require.NoError(t, channel.NewRegistry().Register(a))
	assert.Equal(t, "whatsapp", a.Name())

	caps := a.Capabilities()
	assert.Equal(t, 1500, caps.MaxTextLen, "today's split length (ADR 0003 §3)")
	assert.Equal(t, channel.LightMarkup, caps.Markup)
	assert.Equal(t, []channel.AttachmentKind{channel.KindImage, channel.KindAudio, channel.KindDocument}, caps.AttachmentsIn)
	assert.Zero(t, caps.MaxChoices, "quick replies are V4-10")
	assert.False(t, caps.Groups, "no WhatsApp groups (DE-8)")
	assert.False(t, caps.VoiceOut)
	assert.Contains(t, caps.Proactive, "24 hours")

	in := a.Ingress()
	require.NotNil(t, in.Webhook)
	paths := []string{}
	for _, rt := range in.Webhook.Routes {
		paths = append(paths, rt.Path)
	}
	assert.Equal(t, []string{"/webhook/twilio", "/webhook/twilio/status"}, paths)
}

func TestAdapter_SendGoesToTheNumber(t *testing.T) {
	ctrl := gomock.NewController(t)
	api := mocks.NewMockAPI(ctrl)
	a := NewAdapter(Config{}, WithAPI(api))

	// The text reaches the client unconverted: the client converts and splits it, as before.
	api.EXPECT().SendMessage(gomock.Any(), "+12025550101", "**hi**").Return(nil)
	rcpt, err := a.Send(context.Background(), channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{Text: "**hi**"})
	require.NoError(t, err)
	assert.True(t, rcpt.Accepted)

	_, err = a.Send(context.Background(), channel.Address{Channel: Name, Conversation: "x"}, channel.Outbound{Text: "x"})
	assert.Error(t, err, "no number")

	api.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("twilio API returned status 400"))
	_, err = a.Send(context.Background(), channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{Text: "x"})
	assert.Error(t, err)
}

func TestAdapter_FetchDownloadsTheMediaURL(t *testing.T) {
	ctrl := gomock.NewController(t)
	api := mocks.NewMockAPI(ctrl)
	a := NewAdapter(Config{}, WithAPI(api))
	const u = "https://api.twilio.com/2010-04-01/Accounts/AC1/Messages/SM1/Media/ME1"

	api.EXPECT().DownloadMedia(gomock.Any(), u).Return([]byte("pdf"), nil)
	rc, err := a.Fetch(context.Background(), channel.AttachmentRef{ID: u, Kind: channel.KindDocument})
	require.NoError(t, err)
	data, _ := io.ReadAll(rc)
	assert.Equal(t, "pdf", string(data))
}

func TestAdapter_RecognizesPhoneNumbersOnly(t *testing.T) {
	a := NewAdapter(Config{})
	for _, id := range []string{"+12025550101", "whatsapp:+12025550101"} {
		assert.True(t, a.Recognizes(id), id)
	}
	for _, id := range []string{"", "7001", "-1001", "user-1"} {
		assert.False(t, a.Recognizes(id), id)
	}
}

// Both routes keep the ingress's answers: an unsigned request is refused, a signed one is queued
// as a neutral message, and a delivery report is never queued.
func TestAdapter_WebhookRoutesFailClosedAndQueue(t *testing.T) {
	const token = "route-token"
	routes := NewAdapter(Config{AuthToken: token}).Ingress().Webhook.Routes
	var queued []channel.Inbound
	sink := func(_ context.Context, in channel.Inbound) error { queued = append(queued, in); return nil }
	post := func(route channel.Route, path string, form url.Values, signed bool) int {
		full := "https://mypa.example" + path
		req := httptest.NewRequest(http.MethodPost, full, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if signed {
			req.Header.Set("X-Twilio-Signature", signForm(token, full, form))
		}
		rr := httptest.NewRecorder()
		route.Handler(sink).ServeHTTP(rr, req)
		return rr.Code
	}
	msg := url.Values{"From": {"whatsapp:+12025550101"}, "Body": {"hi"}, "MessageSid": {"SM1"}}

	assert.Equal(t, http.StatusForbidden, post(routes[0], WebhookPath, msg, false))
	assert.Empty(t, queued)
	assert.Equal(t, http.StatusOK, post(routes[0], WebhookPath, msg, true))
	require.Len(t, queued, 1)
	assert.Equal(t, channel.Inbound{Channel: Name, MessageID: "SM1", Sender: "+12025550101",
		Conversation: channel.Conversation{Kind: channel.Direct, Addressed: true}, Text: "hi"}, queued[0])

	report := url.Values{"MessageSid": {"SM2"}, "MessageStatus": {"delivered"}, "To": {"whatsapp:+12025550101"}}
	assert.Equal(t, http.StatusForbidden, post(routes[1], StatusCallbackPath, report, false))
	assert.Equal(t, http.StatusOK, post(routes[1], StatusCallbackPath, report, true))
	assert.Len(t, queued, 1, "a delivery report is never queued as a message")
}
