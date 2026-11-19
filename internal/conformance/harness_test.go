package conformance

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nivik/mypa/internal/calendar"
	layer "github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/state"
	"github.com/nivik/mypa/internal/telegram"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden transcripts in testdata/golden")

// person is someone using MyPA on one channel, as the engine knows them there.
type person struct {
	UserID string // platform user ID: the Telegram user ID, or the WhatsApp phone number
	ChatID string // where replies go on Telegram; empty on WhatsApp, which replies to the number
}

// attachment is a file a person sends.
type attachment struct {
	Kind        string // "photo", "voice" or "document"
	ContentType string
	Data        []byte
}

// inbound is one thing a person does on a channel: a text, or a file with an optional caption.
type inbound struct {
	From       person
	Text       string
	Attachment *attachment
	MessageID  string // the platform's ID for the message; assigned when empty
}

// channel is the conformance fixture of one adapter: how to forge a person's action as the
// platform would deliver it, and what the platform will accept back. What the adapter can do is
// not repeated here: scenarios read it from the adapter's declared capabilities (env.caps).
// Everything else about a channel is pinned by its transcripts.
type channel struct {
	// name is the adapter's name: the value the engine sees in channel.Inbound.Channel.
	name string
	// webhookPath is the adapter's route for inbound messages.
	webhookPath string
	// hardLimit is the platform's own maximum length of one message, in characters. The adapter's
	// declared MaxTextLen must not exceed it (TestFixturesMatchRegisteredAdapters).
	hardLimit int
	// person returns the n-th test person on this channel.
	person func(n int) person
	// address is the recipient a reply to p must carry on the wire.
	address func(p person) string
	// messageID returns a platform-shaped message ID.
	messageID func(n int) string
	// receive turns the action into the platform's own webhook payload and sends it, signed as the
	// platform signs it, to the adapter's real webhook route. ok is false when the adapter produced
	// nothing for the engine.
	receive func(e *env, in inbound) (msg layer.Inbound, ok bool)
}

// fixtures holds one fixture per adapter the app registers (adapters.Build), in registration
// order. The suite runs every scenario on every registered adapter and fails when one has no
// fixture, so a new adapter cannot ship without passing (V4-34); a fixture without an adapter
// fails too (TestFixturesMatchRegisteredAdapters).
var fixtures = []channel{
	{
		name:        telegram.Name,
		webhookPath: telegram.WebhookPath,
		hardLimit:   4096,
		person: func(n int) person {
			id := strconv.Itoa(7000 + n)
			return person{UserID: id, ChatID: id} // a private chat has the user's ID
		},
		address:   func(p person) string { return p.ChatID },
		messageID: func(n int) string { return strconv.Itoa(1000 + n) },
		receive:   receiveTelegram,
	},
	{
		name:        twilio.Name,
		webhookPath: twilio.WebhookPath,
		hardLimit:   1600,
		person: func(n int) person {
			return person{UserID: fmt.Sprintf("+1202555010%d", n)} // reserved fictional numbers
		},
		address:   func(p person) string { return "whatsapp:" + p.UserID },
		messageID: func(n int) string { return fmt.Sprintf("SM%032x", n) },
		receive:   receiveWhatsApp,
	},
}

// fixtureFor returns the fixture of the named adapter.
func fixtureFor(name string) (channel, bool) {
	for _, ch := range fixtures {
		if ch.name == name {
			return ch, true
		}
	}
	return channel{}, false
}

// registeredAdapters returns the adapters the app registers, in registration order: the
// production list (adapters.Build), not a copy of it kept in the test.
func registeredAdapters(t *testing.T) []layer.Adapter {
	t.Helper()
	reg, err := adapters.Build(adapters.Config{})
	require.NoError(t, err)
	require.True(t, reg.Sealed(), "the app's registry must be sealed, or an adapter could be added that the suite never runs")
	all := reg.All()
	require.NotEmpty(t, all, "the app registers no channel adapter")
	return all
}

// receiveTelegram builds a Telegram Update and posts it, with the secret token Telegram sends, to
// the adapter's webhook route.
func receiveTelegram(e *env, in inbound) (layer.Inbound, bool) {
	e.t.Helper()
	userID, err := strconv.ParseInt(in.From.UserID, 10, 64)
	require.NoError(e.t, err)
	chatID, err := strconv.ParseInt(in.From.ChatID, 10, 64)
	require.NoError(e.t, err)
	messageID, err := strconv.Atoi(in.MessageID)
	require.NoError(e.t, err)

	message := map[string]any{
		"message_id": messageID,
		"from":       map[string]any{"id": userID, "username": "conformance"},
		"chat":       map[string]any{"id": chatID},
		"date":       1763500000,
	}
	if in.Attachment == nil {
		message["text"] = in.Text
	} else {
		if in.Text != "" {
			message["caption"] = in.Text
		}
		switch in.Attachment.Kind {
		case "photo":
			// Telegram offers several sizes, smallest first. Only the largest exists on the fake
			// platform, so fetching any other size fails the scenario.
			small, large := "photo-small-"+in.MessageID, "photo-large-"+in.MessageID
			e.wire.addTelegramFile(large, in.Attachment.Data)
			message["photo"] = []map[string]any{
				{"file_id": small, "file_unique_id": "s" + in.MessageID, "width": 90, "height": 67},
				{"file_id": large, "file_unique_id": "l" + in.MessageID, "width": 1280, "height": 960},
			}
		case "voice":
			fileID := "voice-" + in.MessageID
			e.wire.addTelegramFile(fileID, in.Attachment.Data)
			message["voice"] = map[string]any{"file_id": fileID, "duration": 3, "mime_type": in.Attachment.ContentType}
		case "document":
			fileID := "document-" + in.MessageID
			e.wire.addTelegramFile(fileID, in.Attachment.Data)
			message["document"] = map[string]any{"file_id": fileID, "file_name": "file", "mime_type": in.Attachment.ContentType}
		default:
			e.t.Fatalf("unknown attachment kind %q", in.Attachment.Kind)
		}
	}
	body, err := json.Marshal(map[string]any{"update_id": 500000 + messageID, "message": message})
	require.NoError(e.t, err)

	req := httptest.NewRequest(http.MethodPost, "https://mypa.example"+telegram.WebhookPath, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", telegramSecretToken)
	msg, ok := e.post(req)

	// V4-40: an install with no public address polls instead. The same update, fetched with
	// getUpdates, must give the engine exactly the same message, so every scenario holds for both.
	polled, polledOK := e.poll(body, int64(500000+messageID))
	require.Equal(e.t, ok, polledOK, "polling and the webhook must agree on whether this update carries a message")
	require.Equal(e.t, msg, polled, "polling must hand the engine the message the webhook does")
	return msg, ok
}

// poll holds the update on the fake Bot API, runs the polling adapter's outward ingress until it
// has confirmed that update, and returns what it handed to the inbound queue. The poller confirms
// an update only after the sink took its message, so the queue is complete at that point.
func (e *env) poll(update []byte, updateID int64) (layer.Inbound, bool) {
	e.t.Helper()
	adapter, ok := e.polling.Get(e.ch.name)
	require.True(e.t, ok, "adapter %q is not registered", e.ch.name)
	outward := adapter.Ingress().Outward
	require.NotNil(e.t, outward, "adapter %q does not poll without a public address", e.ch.name)

	e.wire.queueTelegramUpdate(update)
	queue := &capturedQueue{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- outward.Run(ctx, queue.sink) }()
	require.Eventually(e.t, func() bool { return e.wire.telegramConfirmed(updateID) }, 10*time.Second, time.Millisecond,
		"the poller must fetch and confirm update %d", updateID)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(e.t, err, context.Canceled, "the poller runs until it is stopped")
	case <-time.After(10 * time.Second):
		e.t.Fatal("the poller did not stop")
	}
	published := queue.messages()
	if len(published) == 0 {
		return layer.Inbound{}, false
	}
	require.Len(e.t, published, 1)
	return published[0], true
}

// receiveWhatsApp builds the form Twilio posts for an inbound WhatsApp message, signs it as Twilio
// does and posts it to the adapter's webhook route.
func receiveWhatsApp(e *env, in inbound) (layer.Inbound, bool) {
	e.t.Helper()
	const webhookURL = "https://mypa.example/webhook/twilio"

	form := url.Values{
		"AccountSid": {twilioAccountSID},
		"MessageSid": {in.MessageID},
		"From":       {"whatsapp:" + in.From.UserID},
		"To":         {twilioSender},
		"Body":       {in.Text},
		"NumMedia":   {"0"},
	}
	if in.Attachment != nil {
		mediaPath := "/2010-04-01/Accounts/" + twilioAccountSID + "/Messages/" + in.MessageID + "/Media/ME" + in.MessageID[2:]
		e.wire.addTwilioMedia(mediaPath, in.Attachment.Data)
		form.Set("NumMedia", "1")
		form.Set("MediaUrl0", "https://api.twilio.com"+mediaPath)
		form.Set("MediaContentType0", in.Attachment.ContentType)
	}

	req := httptest.NewRequest(http.MethodPost, webhookURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", twilioSignature(webhookURL, form))
	return e.post(req)
}

// post serves req with the webhook route of the scenario's adapter, taken from the production
// registry, and returns what the adapter handed to the inbound queue. (An adapter with an outward
// ingress hands its forged payload to the fake transport and runs Outward.Run with the same sink
// instead: see poll, which Telegram also goes through.)
func (e *env) post(req *http.Request) (layer.Inbound, bool) {
	e.t.Helper()
	adapter, ok := e.channels.Get(e.ch.name)
	require.True(e.t, ok, "adapter %q is not registered", e.ch.name)
	webhook := adapter.Ingress().Webhook
	require.NotNil(e.t, webhook, "adapter %q has no webhook ingress", e.ch.name)

	queue := &capturedQueue{}
	var handler http.Handler
	for _, rt := range webhook.Routes {
		if rt.Path == e.ch.webhookPath && rt.Method == req.Method {
			handler = rt.Handler(queue.sink)
		}
	}
	require.NotNil(e.t, handler, "adapter %q serves no %s %s", e.ch.name, req.Method, e.ch.webhookPath)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(e.t, http.StatusOK, rec.Code, "the %s webhook must accept a correctly authenticated request", e.ch.name)
	published := queue.messages()
	if len(published) == 0 {
		return layer.Inbound{}, false
	}
	require.Len(e.t, published, 1)
	return published[0], true
}

// twilioSignature computes X-Twilio-Signature as Twilio documents it: HMAC-SHA1 over the URL
// followed by every POST parameter, sorted by name, keyed with the account's auth token.
func twilioSignature(fullURL string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(fullURL)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(form.Get(k))
	}
	mac := hmac.New(sha1.New, []byte(twilioAuthToken))
	mac.Write([]byte(sb.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// capturedQueue stands in for the inbound queue the ingress publishes to.
type capturedQueue struct {
	mu   sync.Mutex
	msgs []layer.Inbound
}

func (q *capturedQueue) sink(_ context.Context, in layer.Inbound) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, in)
	return nil
}

func (q *capturedQueue) messages() []layer.Inbound {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]layer.Inbound(nil), q.msgs...)
}

// env is one scenario's world: a real engine wired to the production channel registry (both real
// adapters with their real platform clients), with the network, the model, the database and
// speech-to-text replaced by fakes.
type env struct {
	t        *testing.T
	ch       channel
	channels *layer.Registry
	// polling is the registry of an install with no public address (V4-40), on the same fake
	// transport: there Telegram is polled instead of calling the webhook.
	polling  *layer.Registry
	engine   *orchestrator.Engine
	wire     *fakePlatforms
	redis    *miniredis.Miniredis
	db       *memoryDB
	model    *scriptedModel
	stt      *fakeTranscriber
	tools    *scenarioTools
	undo     *fakeDurability
	seq      int
	// compared is set once the scenario has compared its transcript (see golden).
	compared bool
}

func newEnv(t *testing.T, ch channel) *env {
	t.Helper()
	wire := newFakePlatforms(t)
	httpClient := &http.Client{Transport: wire, Timeout: 5 * time.Second}

	redis := miniredis.RunT(t)
	store, err := state.NewStore("redis://" + redis.Addr())
	require.NoError(t, err)

	e := &env{
		t:     t,
		ch:    ch,
		wire:  wire,
		redis: redis,
		db:    newMemoryDB(),
		model: &scriptedModel{t: t},
		stt:   &fakeTranscriber{},
		tools: &scenarioTools{},
		undo:  &fakeDurability{},
	}

	oauth := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "conformance",
		ClientSecret: "conformance",
		RedirectURL:  "https://mypa.example/oauth/callback",
	})
	// The registry the app builds (internal/channel/adapters), configured as an install would be,
	// with every platform client on the fake transport.
	e.channels, err = adapters.Build(adapters.Config{
		Telegram: telegram.Config{BotToken: telegramBotToken, SecretToken: telegramSecretToken},
		WhatsApp: twilio.Config{AccountSID: twilioAccountSID, AuthToken: twilioAuthToken, FromNumber: twilioSender},
	}, adapters.WithHTTPClient(httpClient))
	require.NoError(t, err)
	e.polling, err = adapters.Build(adapters.Config{
		Telegram: telegram.Config{BotToken: telegramBotToken, Polling: true},
	}, adapters.WithHTTPClient(httpClient))
	require.NoError(t, err)

	e.engine = orchestrator.NewEngine(
		nil, // no queue consumer: the suite hands messages to the engine directly
		store,
		e.db,
		e.model,
		e.channels,
		oauth,
		nil, nil, nil, nil, // Gmail, Tasks, Contacts, web search: not part of any scenario
		e.stt,
		auditSink{db: e.db},
		"UTC",
	)
	registry := orchestrator.NewRegistry()
	registry.Register(e.tools)
	registry.Register(e.undo)
	e.engine.SetToolRegistry(registry)
	e.engine.WithHealthReporter(scenarioDoctor())

	t.Cleanup(func() {
		e.engine.Wait()
		_ = store.Close()
	})
	return e
}

// member adds the n-th person of this channel as an already paired member.
func (e *env) member(n int, role, familyGroup string) person {
	e.t.Helper()
	p := e.ch.person(n)
	require.NoError(e.t, e.db.UpsertUser(models.User{
		PlatformID:  p.UserID,
		Name:        fmt.Sprintf("Member %d", n),
		Role:        role,
		FamilyGroup: familyGroup,
		CreatedAt:   time.Now(),
	}))
	return p
}

// deliver sends one inbound action through the channel's ingress and, if that produced a message,
// through the engine. It reports whether the engine received anything.
func (e *env) deliver(in inbound) bool {
	e.t.Helper()
	if in.MessageID == "" {
		e.seq++
		in.MessageID = e.ch.messageID(e.seq)
	}
	msg, ok := e.ch.receive(e, in)
	if !ok {
		return false
	}

	// The neutral message every channel must produce.
	assert.Equal(e.t, e.ch.name, msg.Channel, "channel")
	assert.Equal(e.t, in.From.UserID, msg.Sender, "sender")
	assert.Equal(e.t, in.From.ChatID, msg.Conversation.ID, "conversation ID")
	assert.Equal(e.t, in.MessageID, msg.MessageID, "message ID")
	assert.True(e.t, msg.Conversation.Addressed, "a direct message is addressed to the assistant")
	if in.Attachment != nil {
		// A file reaches the engine only as a kind the adapter declares it receives.
		kind := attachmentKind(e.t, in.Attachment.Kind)
		if assert.Len(e.t, msg.Attachments, 1, "the file the person sent") {
			assert.Equal(e.t, kind, msg.Attachments[0].Kind, "attachment kind")
		}
		assert.True(e.t, e.accepts(kind), "%s handed the engine a %s, which it does not declare in AttachmentsIn", e.ch.name, kind)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(e.t, e.engine.ProcessMessage(ctx, msg))
	e.engine.Wait() // the audit session is published in the background
	return true
}

// attachmentKind maps the kind of file a person sends to the channel layer's kind.
func attachmentKind(t *testing.T, kind string) layer.AttachmentKind {
	t.Helper()
	switch kind {
	case "photo":
		return layer.KindImage
	case "voice":
		return layer.KindAudio
	case "document":
		return layer.KindDocument
	}
	t.Fatalf("unknown attachment kind %q", kind)
	return ""
}

// caps returns what the scenario's adapter declares.
func (e *env) caps() layer.Capabilities {
	e.t.Helper()
	adapter, ok := e.channels.Get(e.ch.name)
	require.True(e.t, ok, "adapter %q is not registered", e.ch.name)
	return adapter.Capabilities()
}

// accepts reports whether the scenario's adapter declares that it receives files of this kind.
func (e *env) accepts(kind layer.AttachmentKind) bool {
	return layer.Accepts(e.caps().AttachmentsIn, kind)
}

// declined asserts the documented fallback for a file of a kind the adapter does not declare in
// AttachmentsIn: the adapter drops the message, so nothing reaches the engine, nothing is
// downloaded and nothing is answered (SYSTEM.md §3.10).
func (e *env) declined(scenario string, in inbound) {
	e.t.Helper()
	ok := e.deliver(in)
	assert.False(e.t, ok, "a %s must not reach the engine on %s, which does not declare it", in.Attachment.Kind, e.ch.name)
	assert.Empty(e.t, e.wire.fetched(), "the file must not be downloaded")
	assert.Empty(e.t, e.model.requests())
	assert.Empty(e.t, e.wire.messages())
	e.golden(scenario)
}

// says delivers a plain text message and requires that it reached the engine.
func (e *env) says(p person, text string) {
	e.t.Helper()
	require.True(e.t, e.deliver(inbound{From: p, Text: text}), "a text message must reach the engine")
}

// texts returns the text of every message sent so far.
func (e *env) texts() []string {
	var out []string
	for _, m := range e.wire.messages() {
		out = append(out, m.Text)
	}
	return out
}

// lastText returns the most recent message sent.
func (e *env) lastText() string {
	e.t.Helper()
	texts := e.texts()
	require.NotEmpty(e.t, texts, "nothing was sent")
	return texts[len(texts)-1]
}

// requireAllSentTo asserts that every message went out on this scenario's channel and only to
// the given people: an answer never crosses to another platform or another person.
func (e *env) requireAllSentTo(people ...person) {
	e.t.Helper()
	allowed := map[string]bool{}
	for _, p := range people {
		allowed[e.ch.address(p)] = true
	}
	for i, m := range e.wire.messages() {
		assert.Equal(e.t, e.ch.name, m.Platform, "message %d went out on the wrong platform", i+1)
		assert.True(e.t, allowed[m.To], "message %d went to %q, which is not a participant of the scenario", i+1, m.To)
	}
}

// transcript renders everything sent to users, exactly as the platform APIs received it.
func (e *env) transcript() string {
	msgs := e.wire.messages()
	if len(msgs) == 0 {
		return "(nothing was sent)\n"
	}
	var b strings.Builder
	for i, m := range msgs {
		fmt.Fprintf(&b, "=== message %d: %s to=%s %s\n%s\n", i+1, m.Platform, m.To, m.Meta, m.Text)
	}
	return b.String()
}

func goldenPath(scenario, channelName string) string {
	return filepath.Join("testdata", "golden", scenario+"."+channelName+".txt")
}

// golden compares the scenario's transcript with the pinned one (or rewrites it with -update).
func (e *env) golden(scenario string) {
	e.t.Helper()
	path := goldenPath(scenario, e.ch.name)
	e.compared = true
	got := e.transcript()

	if *update {
		require.NoError(e.t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(e.t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	raw, err := os.ReadFile(path)
	require.NoError(e.t, err, "no pinned transcript for this scenario; create it with: go test ./internal/conformance -update")
	want := strings.ReplaceAll(string(raw), "\r\n", "\n") // a Windows checkout may convert line endings
	assert.Equal(e.t, want, got, "what users receive on %s changed. If that is intended, rerun with -update and review the diff.", e.ch.name)
}

// finish runs the checks every scenario ends with.
func (e *env) finish() {
	e.t.Helper()
	e.engine.Wait()
	assert.Zero(e.t, e.model.unusedSteps(), "the scenario scripted model answers that were never requested")
}
