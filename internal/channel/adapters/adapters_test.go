package adapters

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extra is an adapter that is not on the shipped list.
type extra struct{}

func (extra) Name() string { return "extra" }
func (extra) Capabilities() channel.Capabilities {
	return channel.Capabilities{MaxTextLen: 100, Markup: channel.PlainText, Proactive: "always"}
}
func (extra) Ingress() channel.Ingress {
	return channel.Ingress{Webhook: &channel.Webhook{Routes: []channel.Route{{
		Method: http.MethodPost, Path: channel.WebhookPrefix + "extra",
		Handler: func(channel.Sink) http.Handler { return http.NotFoundHandler() },
	}}}}
}
func (extra) Send(context.Context, channel.Address, channel.Outbound) (channel.Receipt, error) {
	return channel.Receipt{Accepted: true}, nil
}
func (extra) Fetch(context.Context, channel.AttachmentRef) (io.ReadCloser, error) { return nil, io.EOF }

// The registry Build returns is sealed (V4-34): an adapter that is not on the shipped list, and so
// not run by the conformance suite, cannot be added to it afterwards.
func TestBuild_ReturnsASealedRegistry(t *testing.T) {
	reg, err := Build(Config{})
	require.NoError(t, err)
	assert.True(t, reg.Sealed())
	before := len(reg.All())
	require.NotZero(t, before)

	assert.Error(t, reg.Register(extra{}))
	assert.Len(t, reg.All(), before)
	_, ok := reg.Get("extra")
	assert.False(t, ok)
}
