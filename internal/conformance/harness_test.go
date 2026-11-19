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

// channel is what the suite needs to know about one platform: how a person's action arrives, and
// what the platform will accept back. Everything else about a channel is pinned by its transcripts.
type channel struct {
	// name is the value the engine sees in models.Message.Source.
	name string
	// hardLimit is the platform's own maximum length of one message, in characters.
	hardLimit int
	// ingestsDocuments says whether a file sent as a document reaches the assistant today.
	// WhatsApp: yes. Telegram: no, the update is dropped at the gateway (see SYSTEM.md).
	ingestsDocuments bool
	// person returns the n-th test person on this channel.
	person func(n int) person
	// address is the recipient a reply to p must carry on the wire.
	address func(p person) string
	// messageID returns a platform-shaped message ID.
	messageID func(n int) string
	// receive turns the action into the platform's own webhook payload and runs it through the
	// platform's real ingress code. ok is false when that code produced nothing for the engine.
	receive func(e *env, in inbound) (msg models.Message, ok bool)
}

// channels lists every supported channel. A scenario runs on all of them.
var channels = []channel{
	{
		name:             "telegram",
		hardLimit:        4096,
		ingestsDocuments: false,
		person: func(n int) person {
			id := strconv.Itoa(7000 + n)
			return person{UserID: id, ChatID: id} // a private chat has the user's ID
		},
		address:   func(p person) string { return p.ChatID },
		messageID: func(n int) string { return strconv.Itoa(1000 + n) },
		receive:   receiveTelegram,
	},
	{
		name:             "whatsapp",
		hardLimit:        1600,
		ingestsDocuments: true,
		person: func(n int) person {
			return person{UserID: fmt.Sprintf("+1202555010%d", n)} // reserved fictional numbers
		},
		address:   func(p person) string { return "whatsapp:" + p.UserID },
		messageID: func(n int) string { return fmt.Sprintf("SM%032x", n) },
		receive:   receiveWhatsApp,
	},
}

// receiveTelegram builds a Telegram Update and parses it with the real parser. The webhook's
// secret-token check lives in cmd/gateway and is pinned by that package's own tests.
func receiveTelegram(e *env, in inbound) (models.Message, bool) {
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

	msg, err := telegram.ParseUpdate(body)
	require.NoError(e.t, err)
	if msg == nil {
		return models.Message{}, false
	}
	return *msg, true
}

// receiveWhatsApp builds the form Twilio posts for an inbound WhatsApp message, signs it as Twilio
// does and sends it through the real webhook handler.
func receiveWhatsApp(e *env, in inbound) (models.Message, bool) {
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
	rec := httptest.NewRecorder()
	queue := &capturedQueue{}

	twilio.NewHandler(queue, twilioAuthToken).HandleWebhook(rec, req)

	require.Equal(e.t, http.StatusOK, rec.Code, "the Twilio webhook must accept a correctly signed message")
	published := queue.messages()
	if len(published) == 0 {
		return models.Message{}, false
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

// capturedQueue stands in for the inbound queue the gateway publishes to.
type capturedQueue struct {
	mu   sync.Mutex
	msgs []models.Message
}

func (q *capturedQueue) Publish(_ context.Context, v any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if m, ok := v.(models.Message); ok {
		q.msgs = append(q.msgs, m)
	}
	return nil
}

func (q *capturedQueue) messages() []models.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]models.Message(nil), q.msgs...)
}

// env is one scenario's world: a real engine wired to the real clients of both platforms, with
// the network, the model, the database and speech-to-text replaced by fakes.
type env struct {
	t      *testing.T
	ch     channel
	engine *orchestrator.Engine
	wire   *fakePlatforms
	redis  *miniredis.Miniredis
	db     *memoryDB
	model  *scriptedModel
	stt    *fakeTranscriber
	tools  *scenarioTools
	undo   *fakeDurability
	seq    int
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
	e.engine = orchestrator.NewEngine(
		nil, // no queue consumer: the suite hands messages to the engine directly
		store,
		e.db,
		e.model,
		telegram.NewClient(telegramBotToken, telegram.WithHTTPClient(httpClient)),
		twilio.NewClient(config.TwilioConfig{
			AccountSID: twilioAccountSID,
			AuthToken:  twilioAuthToken,
			FromNumber: twilioSender,
		}, twilio.WithHTTPClient(httpClient)),
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
	assert.Equal(e.t, e.ch.name, msg.Source, "source")
	assert.Equal(e.t, in.From.UserID, msg.UserID, "user ID")
	assert.Equal(e.t, in.From.ChatID, msg.ChatID, "chat ID")
	assert.Equal(e.t, in.MessageID, msg.ID, "message ID")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(e.t, e.engine.ProcessMessage(ctx, msg))
	e.engine.Wait() // the audit session is published in the background
	return true
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
