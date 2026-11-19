package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdapter is a configurable adapter for the registry's own tests.
type fakeAdapter struct {
	name    string
	caps    Capabilities
	ingress Ingress
	receipt Receipt
	sendErr error
	sent    []Outbound
	files   map[string]string
	owns    func(string) bool
}

func (f *fakeAdapter) Name() string               { return f.name }
func (f *fakeAdapter) Capabilities() Capabilities { return f.caps }
func (f *fakeAdapter) Ingress() Ingress           { return f.ingress }
func (f *fakeAdapter) Send(_ context.Context, _ Address, m Outbound) (Receipt, error) {
	f.sent = append(f.sent, m)
	return f.receipt, f.sendErr
}
func (f *fakeAdapter) Fetch(_ context.Context, a AttachmentRef) (io.ReadCloser, error) {
	data, ok := f.files[a.ID]
	if !ok {
		return nil, errors.New("no such file")
	}
	return io.NopCloser(strings.NewReader(data)), nil
}

type recognizingAdapter struct{ *fakeAdapter }

func (r recognizingAdapter) Recognizes(p string) bool { return r.owns(p) }

func validCaps() Capabilities {
	return Capabilities{MaxTextLen: 1000, Markup: PlainText, Proactive: "always"}
}

func webhookAt(paths ...string) Ingress {
	wh := &Webhook{}
	for _, p := range paths {
		wh.Routes = append(wh.Routes, Route{Method: http.MethodPost, Path: p, Handler: func(Sink) http.Handler { return http.NotFoundHandler() }})
	}
	return Ingress{Webhook: wh}
}

func newFake(name string, paths ...string) *fakeAdapter {
	if len(paths) == 0 {
		paths = []string{WebhookPrefix + name}
	}
	return &fakeAdapter{name: name, caps: validCaps(), ingress: webhookAt(paths...), receipt: Receipt{Accepted: true}}
}

func TestRegister_RejectsInvalidDeclarations(t *testing.T) {
	outward := Ingress{Outward: &Outward{Run: func(context.Context, Sink) error { return nil }}}
	cases := map[string]func(a *fakeAdapter){
		"empty name":            func(a *fakeAdapter) { a.name = "" },
		"upper-case name":       func(a *fakeAdapter) { a.name = "Chat" },
		"no text length":        func(a *fakeAdapter) { a.caps.MaxTextLen = 0 },
		"unknown markup":        func(a *fakeAdapter) { a.caps.Markup = "rtf" },
		"no proactive rule":     func(a *fakeAdapter) { a.caps.Proactive = "" },
		"buttons without label": func(a *fakeAdapter) { a.caps.MaxChoices = 3 },
		"negative buttons":      func(a *fakeAdapter) { a.caps.MaxChoices = -1 },
		"unknown attachment":    func(a *fakeAdapter) { a.caps.AttachmentsIn = []AttachmentKind{"video"} },
		"no ingress":            func(a *fakeAdapter) { a.ingress = Ingress{} },
		"two ingresses":         func(a *fakeAdapter) { a.ingress.Outward = outward.Outward },
		"webhook without route": func(a *fakeAdapter) { a.ingress = Ingress{Webhook: &Webhook{}} },
		"route off the prefix":  func(a *fakeAdapter) { a.ingress = webhookAt("/hook/x") },
		"bare prefix route":     func(a *fakeAdapter) { a.ingress = webhookAt(WebhookPrefix) },
		"route without handler": func(a *fakeAdapter) { a.ingress.Webhook.Routes[0].Handler = nil },
		"route twice":           func(a *fakeAdapter) { a.ingress = webhookAt(WebhookPrefix+"x", WebhookPrefix+"x") },
		"outward without run":   func(a *fakeAdapter) { a.ingress = Ingress{Outward: &Outward{}} },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			a := newFake("chat")
			breakIt(a)
			err := NewRegistry().Register(a)
			require.Error(t, err)
			assert.Empty(t, NewRegistry().All())
		})
	}
	require.Error(t, NewRegistry().Register(nil))
}

func TestRegister_NamesAndRoutesAreUnique(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(newFake("one")))
	assert.Error(t, r.Register(newFake("one", WebhookPrefix+"other")), "a second adapter with the same name")
	assert.Error(t, r.Register(newFake("two", WebhookPrefix+"one")), "a route another adapter serves")
	require.NoError(t, r.Register(newFake("two")))

	names := []string{}
	for _, a := range r.All() {
		names = append(names, a.Name())
	}
	assert.Equal(t, []string{"one", "two"}, names, "registration order is kept")
	assert.Equal(t, []string{WebhookPrefix + "one", WebhookPrefix + "two"}, r.WebhookPaths())
}

func TestRegister_AcceptsAnOutwardIngress(t *testing.T) {
	a := newFake("socket")
	a.ingress = Ingress{Outward: &Outward{Run: func(context.Context, Sink) error { return nil }}}
	r := NewRegistry()
	require.NoError(t, r.Register(a))
	assert.False(t, a.Ingress().NeedsPublicAddress())
	assert.Equal(t, "outward", a.Ingress().Mode())
	assert.Empty(t, r.WebhookPaths())
}

func TestSend_RendersForTheDestinationAndReportsRefusals(t *testing.T) {
	r := NewRegistry()
	a := newFake("chat")
	require.NoError(t, r.Register(a))
	ctx := context.Background()

	_, err := r.Send(ctx, Address{Channel: "elsewhere"}, Outbound{Text: "hi"})
	assert.ErrorIs(t, err, ErrUnknownChannel)

	_, err = r.Send(ctx, Address{Channel: "chat"}, Outbound{Text: "pick", Choices: []Choice{{ID: "yes", Label: "Yes"}}})
	require.NoError(t, err)
	require.Len(t, a.sent, 1)
	assert.Empty(t, a.sent[0].Choices, "an adapter without buttons never receives choices")
	assert.Equal(t, "pick\n\n1. Yes", a.sent[0].Text)

	_, err = r.Send(ctx, Address{Channel: "chat"}, Outbound{Choices: []Choice{{ID: strings.Repeat("x", MaxChoiceIDLen+1), Label: "Too long"}}})
	assert.Error(t, err, "a choice ID over the limit is refused before the adapter is called")
	assert.Len(t, a.sent, 1)

	a.receipt = Receipt{Reason: WindowClosed}
	_, err = r.Send(ctx, Address{Channel: "chat"}, Outbound{Text: "later"})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, WindowClosed, refused.Reason)

	a.receipt, a.sendErr = Receipt{}, errors.New("network down")
	assert.EqualError(t, r.SendText(ctx, Address{Channel: "chat"}, "x"), "network down")
}

func TestFetchAll_ReadsFromTheAdapterThatIssuedTheReference(t *testing.T) {
	r := NewRegistry()
	a := newFake("chat")
	a.files = map[string]string{"f1": "hello"}
	require.NoError(t, r.Register(a))

	data, err := r.FetchAll(context.Background(), "chat", AttachmentRef{ID: "f1"})
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	_, err = r.FetchAll(context.Background(), "other", AttachmentRef{ID: "f1"})
	assert.ErrorIs(t, err, ErrUnknownChannel)
	_, err = r.FetchAll(context.Background(), "chat", AttachmentRef{ID: "missing"})
	assert.Error(t, err)
}

func TestRecognize_AsksAdaptersInRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	plus := recognizingAdapter{newFake("phone")}
	plus.owns = func(p string) bool { return strings.HasPrefix(p, "+") }
	any := recognizingAdapter{newFake("numeric")}
	any.owns = func(p string) bool { return p != "" && strings.Trim(p, "0123456789") == "" }
	require.NoError(t, r.Register(plus))
	require.NoError(t, r.Register(any))
	require.NoError(t, r.Register(newFake("silent"))) // no Recognizer: never chosen

	addr, ok := r.Recognize("+15550100")
	require.True(t, ok)
	assert.Equal(t, Address{Channel: "phone", Person: "+15550100"}, addr)
	addr, ok = r.Recognize("12345")
	require.True(t, ok)
	assert.Equal(t, "numeric", addr.Channel)
	_, ok = r.Recognize("someone@example")
	assert.False(t, ok)
	_, ok = r.Recognize(" ")
	assert.False(t, ok)
}

func TestNilRegistryIsEmpty(t *testing.T) {
	var r *Registry
	_, ok := r.Get("x")
	assert.False(t, ok)
	assert.Empty(t, r.All())
	assert.Empty(t, r.WebhookPaths())
	_, ok = r.Recognize("x")
	assert.False(t, ok)
}
