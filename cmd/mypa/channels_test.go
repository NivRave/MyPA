package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/channel/adapters/adapterstest"
	tgmocks "github.com/nivik/mypa/internal/telegram/mocks"
	wamocks "github.com/nivik/mypa/internal/twilio/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// Partner notifications, automations and the Google sign-in confirmation know a person only by
// their ID. The adapter that recognizes the ID sends (V4-31), as the leading-"+" guess in the
// wiring did before; the Google confirmation now reaches WhatsApp users too.
func TestChannelNotifier_SendsOnTheChannelThatRecognizesTheRecipient(t *testing.T) {
	ctrl := gomock.NewController(t)
	tg := tgmocks.NewMockAPI(ctrl)
	wa := wamocks.NewMockAPI(ctrl)
	n := channelNotifier{channels: adapterstest.Registry(tg, wa)}
	ctx := context.Background()

	wa.EXPECT().SendMessage(gomock.Any(), "+12025550101", "partner logged a feeding").Return(nil)
	require.NoError(t, n.SendMessage(ctx, "+12025550101", "partner logged a feeding"))

	wa.EXPECT().SendMessage(gomock.Any(), "whatsapp:+12025550101", "x").Return(nil)
	require.NoError(t, n.SendMessage(ctx, "whatsapp:+12025550101", "x"))

	tg.EXPECT().SendMessage(gomock.Any(), "7001", "<b>done</b>").Return(nil)
	require.NoError(t, n.SendMessage(ctx, "7001", "**done**"))

	assert.Error(t, n.SendMessage(ctx, "someone@example", "x"), "an ID no adapter recognizes is not guessed")
}

// The ingress serves every registered adapter's webhook routes and nothing it does not register.
func TestIngressRouter_MountsEveryAdapterRoute(t *testing.T) {
	reg, err := adapters.Build(adapters.Config{})
	require.NoError(t, err)
	r := setupIngressRouter(new(MockEventPublisher), reg) // unconfigured adapters never publish

	paths := reg.WebhookPaths()
	require.NotEmpty(t, paths)
	for _, path := range paths {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		assert.NotEqual(t, http.StatusNotFound, rr.Code, "%s must be routed to its adapter", path)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rr.Code, "%s fails closed when unconfigured", path)
	}

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/webhook/unknown", strings.NewReader("{}")))
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
