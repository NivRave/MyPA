package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUpdates is the Bot API as the poller sees it: it holds updates until a getUpdates call
// confirms them with a higher offset, as Telegram does.
type fakeUpdates struct {
	mu            sync.Mutex
	pending       []json.RawMessage
	deleteErrs    []error // returned by successive deleteWebhook calls, then nil
	getErrs       []error // returned by successive getUpdates calls, then the updates
	deleted       int
	offsets       []int64
	timeouts      []time.Duration
	callsAfterDel bool // every getUpdates came after a successful deleteWebhook
}

func update(id int, text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"from":{"id":42},"chat":{"id":42},"date":1763500000,"text":%q}}`, id, id+1000, text))
}

func (f *fakeUpdates) DeleteWebhook(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleteErrs) > 0 {
		err := f.deleteErrs[0]
		f.deleteErrs = f.deleteErrs[1:]
		return err
	}
	f.deleted++
	f.callsAfterDel = true
	return nil
}

func (f *fakeUpdates) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]json.RawMessage, error) {
	f.mu.Lock()
	f.offsets = append(f.offsets, offset)
	f.timeouts = append(f.timeouts, timeout)
	if f.deleted == 0 {
		f.callsAfterDel = false
	}
	if len(f.getErrs) > 0 {
		err := f.getErrs[0]
		f.getErrs = f.getErrs[1:]
		f.mu.Unlock()
		return nil, err
	}
	// Telegram forgets every update below the offset it was given.
	var keep []json.RawMessage
	for _, u := range f.pending {
		id, _ := updateID(u)
		if id >= offset {
			keep = append(keep, u)
		}
	}
	f.pending = keep
	out := append([]json.RawMessage(nil), keep...)
	f.mu.Unlock()
	if len(out) == 0 {
		// A long poll with nothing to return: wait a little, as Telegram would wait the timeout.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return out, nil
}

func (f *fakeUpdates) remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	last := int64(0)
	if len(f.offsets) > 0 {
		last = f.offsets[len(f.offsets)-1]
	}
	for _, u := range f.pending {
		if id, _ := updateID(u); id >= last {
			n++
		}
	}
	return n
}

// recordingSink collects what the poller hands to the inbound queue; it fails while failing > 0.
type recordingSink struct {
	mu      sync.Mutex
	got     []channel.Inbound
	failing int
	tries   int
}

func (s *recordingSink) sink(_ context.Context, in channel.Inbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tries++
	if s.failing > 0 {
		s.failing--
		return errors.New("broker down")
	}
	s.got = append(s.got, in)
	return nil
}

func (s *recordingSink) messages() []channel.Inbound {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]channel.Inbound(nil), s.got...)
}

func testPoller(api updatesAPI, sink channel.Sink) *poller {
	return &poller{api: api, sink: sink, timeout: pollTimeout, baseDelay: time.Millisecond, maxDelay: 4 * time.Millisecond}
}

// runUntil runs the poller until cond holds, then stops it and returns what Run returned.
func runUntil(t *testing.T, p *poller, cond func() bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.run(ctx) }()
	require.Eventually(t, cond, 5*time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the poller did not stop when its context ended")
		return nil
	}
}

// V4-40: with no public address, the adapter fetches updates itself. It removes any webhook first
// (Telegram refuses getUpdates while one is set) without dropping what is waiting, hands each
// message to the sink in order, and confirms each update by asking for the next offset.
func TestPoller_RemovesTheWebhookThenDeliversEveryMessageInOrder(t *testing.T) {
	api := &fakeUpdates{pending: []json.RawMessage{update(10, "first"), update(11, "second"), update(12, "third")}}
	sink := &recordingSink{}
	err := runUntil(t, testPoller(api, sink.sink), func() bool { return len(sink.messages()) == 3 && api.remaining() == 0 })
	assert.ErrorIs(t, err, context.Canceled)

	var texts []string
	for _, m := range sink.messages() {
		texts = append(texts, m.Text)
		assert.Equal(t, Name, m.Channel)
		assert.Equal(t, "42", m.Sender)
	}
	assert.Equal(t, []string{"first", "second", "third"}, texts)

	api.mu.Lock()
	defer api.mu.Unlock()
	assert.Equal(t, 1, api.deleted)
	assert.True(t, api.callsAfterDel, "getUpdates is only called once the webhook is gone")
	assert.Equal(t, int64(0), api.offsets[0], "the first call starts from what Telegram still holds")
	assert.Contains(t, api.offsets, int64(13), "the last update is confirmed")
	assert.Equal(t, pollTimeout, api.timeouts[0], "a long poll, not a busy loop")
}

// A message that could not be queued is not confirmed: the poller waits and fetches it again, so
// it is delivered once the queue is back, as a webhook that answered 500 would be retried.
func TestPoller_DoesNotConfirmAMessageItCouldNotQueue(t *testing.T) {
	api := &fakeUpdates{pending: []json.RawMessage{update(20, "kept"), update(21, "after")}}
	sink := &recordingSink{failing: 2}
	_ = runUntil(t, testPoller(api, sink.sink), func() bool { return len(sink.messages()) == 2 })

	msgs := sink.messages()
	assert.Equal(t, "kept", msgs[0].Text, "nothing is lost or reordered")
	assert.Equal(t, "after", msgs[1].Text)
	sink.mu.Lock()
	assert.Equal(t, 4, sink.tries)
	sink.mu.Unlock()
	api.mu.Lock()
	defer api.mu.Unlock()
	assert.NotContains(t, api.offsets[:3], int64(21), "update 20 is not confirmed while it could not be queued")
}

// Updates that carry nothing for the assistant, or that cannot be read, are confirmed and
// dropped, exactly as the webhook answers them 200.
func TestPoller_ConfirmsUpdatesThatCarryNothing(t *testing.T) {
	api := &fakeUpdates{pending: []json.RawMessage{
		json.RawMessage(`{"update_id":30,"edited_message":{"message_id":1}}`),
		json.RawMessage(`{"update_id":31,"message":"not an object"}`),
		update(32, "real"),
	}}
	sink := &recordingSink{}
	_ = runUntil(t, testPoller(api, sink.sink), func() bool { return len(sink.messages()) == 1 && api.remaining() == 0 })
	assert.Equal(t, "real", sink.messages()[0].Text)
}

// Failures to reach Telegram never stop the poller: it backs off and tries again, both for
// removing the webhook and for fetching. A conflict (another process polling the same bot, or a
// webhook set again) is reported so the operator can find the other install.
func TestPoller_RetriesUntilTelegramAnswers(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	api := &fakeUpdates{
		pending:    []json.RawMessage{update(40, "eventually")},
		deleteErrs: []error{errors.New("dial tcp: timeout"), errors.New("dial tcp: timeout")},
		getErrs:    []error{ErrConflict, errors.New("502")},
	}
	sink := &recordingSink{}
	_ = runUntil(t, testPoller(api, sink.sink), func() bool { return len(sink.messages()) == 1 })
	assert.Equal(t, "eventually", sink.messages()[0].Text)
	assert.Contains(t, logs.String(), "another install", "a conflict names its likely cause")
}

func TestPoller_BackoffGrowsAndIsCapped(t *testing.T) {
	p := &poller{baseDelay: time.Second, maxDelay: 5 * time.Second}
	var got []time.Duration
	d := p.baseDelay
	for i := 0; i < 5; i++ {
		got = append(got, d)
		d = p.next(d)
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}, got)
}

// P3-3: no message content in the log at INFO, on this path as on the webhook.
func TestPoller_LogsNoMessageTextAtInfo(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	api := &fakeUpdates{pending: []json.RawMessage{update(50, "my secret medical question")}}
	sink := &recordingSink{}
	_ = runUntil(t, testPoller(api, sink.sink), func() bool { return len(sink.messages()) == 1 })
	assert.NotContains(t, logs.String(), "medical")
	assert.Contains(t, logs.String(), "text_len")
}

// The adapter chooses its ingress from its settings: with a public address Telegram calls the
// webhook; without one the adapter polls, and needs no secret token, because no route is served.
func TestAdapter_IngressFollowsThePublicAddress(t *testing.T) {
	webhook := NewAdapter(Config{BotToken: "t", SecretToken: "s", WebhookURL: "https://mypa.example.com" + WebhookPath})
	assert.Equal(t, "webhook", webhook.Ingress().Mode())
	assert.True(t, webhook.Ingress().NeedsPublicAddress())

	polling := NewAdapter(Config{BotToken: "t", Polling: true})
	in := polling.Ingress()
	assert.Equal(t, "outward", in.Mode())
	assert.False(t, in.NeedsPublicAddress())
	require.NotNil(t, in.Outward.Run)
	assert.Nil(t, in.Webhook, "a polling install serves no Telegram route at all")

	assert.Empty(t, Config{BotToken: "t", Polling: true}.Validate(true), "no secret token is needed to poll")
	assert.NotEmpty(t, Config{BotToken: "t"}.Validate(true), "the webhook still fails closed without one")
}

// An unconfigured adapter in polling mode does nothing: no token, no calls to Telegram.
func TestAdapter_UnconfiguredPollingReturnsAtOnce(t *testing.T) {
	a := NewAdapter(Config{Polling: true}, WithClient(NewClient("", WithHTTPClient(refusingClient(t)))))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, a.Ingress().Outward.Run(ctx, func(context.Context, channel.Inbound) error {
		t.Error("nothing may be delivered")
		return nil
	}))
	assert.False(t, a.Configured())
	assert.True(t, NewAdapter(Config{BotToken: "t", Polling: true}).Configured())
}

func TestLoadConfig_DerivesTheModeAndAddressFromTheDeploySettings(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

	local := LoadConfig(env(map[string]string{"TELEGRAM_BOT_TOKEN": "t"}), deploy.Load(env(nil)))
	assert.True(t, local.Polling)
	assert.Empty(t, local.WebhookURL)

	server := LoadConfig(env(map[string]string{"TELEGRAM_BOT_TOKEN": "t"}),
		deploy.Load(env(map[string]string{"INGRESS": "caddy", "PUBLIC_URL": "https://mypa.example.com"})))
	assert.False(t, server.Polling)
	assert.Equal(t, "https://mypa.example.com/webhook/telegram", server.WebhookURL)
}

// refusingClient fails the test on any request: the code under test must not call Telegram.
func refusingClient(t *testing.T) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		return nil, errors.New("refused")
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ignoringAPI answers every call at once and ignores cancellation, as a fake or a misbehaving
// transport may: the poller must still stop when its context ends.
type ignoringAPI struct{}

func (ignoringAPI) DeleteWebhook(context.Context) error { return nil }
func (ignoringAPI) GetUpdates(context.Context, int64, time.Duration) ([]json.RawMessage, error) {
	return nil, nil
}

func TestPoller_StopsEvenWhenTheTransportIgnoresCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- testPoller(ignoringAPI{}, (&recordingSink{}).sink).run(ctx) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the poller kept going after its context ended")
	}
}
