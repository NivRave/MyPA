package twilio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testReminderSID = "HX0123456789abcdef0123456789abcdef"
	testBriefingSID = "HXfedcba9876543210fedcba9876543210"
)

// fakeMessages is Twilio's Messages API: it records each form and answers with a fresh SID, or
// with the error the test set.
type fakeMessages struct {
	mu     sync.Mutex
	forms  []url.Values
	status int
	answer string
}

func (f *fakeMessages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forms = append(f.forms, r.PostForm)
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.answer))
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"sid": "SM" + strings.Repeat("0", 31) + string(rune('0'+len(f.forms)))})
}

func adapterOn(t *testing.T, cfg Config, f *fakeMessages) *Adapter {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	client := NewClient(cfg.Effective())
	client.httpClient.Transport = &rewriteTransport{URL: ts.URL}
	return NewAdapter(cfg, WithAPI(client))
}

func senderConfig() Config {
	return Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: "whatsapp:+12025550199", Mode: ModeSender,
		TemplateSIDs: map[string]string{TemplateReminder: testReminderSID, TemplateBriefing: testBriefingSID}}
}

func sandboxConfig() Config {
	return Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: SandboxSender, Mode: ModeSandbox}
}

// The rule each mode declares (V4-33): a 24-hour window in both; the sandbox adds the 72-hour
// membership and has no templates; a sender lists the templates it has a Content SID for.
func TestCapabilities_ProactiveRulePerMode(t *testing.T) {
	sandbox := NewAdapter(sandboxConfig()).Capabilities().Rule
	assert.Equal(t, channel.ProactiveRule{Window: Window, Reach: SandboxMembership}, sandbox)

	sender := NewAdapter(senderConfig()).Capabilities().Rule
	assert.Equal(t, Window, sender.Window)
	assert.Zero(t, sender.Reach)
	assert.Equal(t, []string{TemplateReminder, TemplateBriefing}, sender.Templates)

	partial := senderConfig()
	partial.TemplateSIDs = map[string]string{TemplateReminder: testReminderSID}
	assert.Equal(t, []string{TemplateReminder}, NewAdapter(partial).Capabilities().Rule.Templates,
		"a template without a Content SID cannot be sent")
	for _, a := range []*Adapter{NewAdapter(sandboxConfig()), NewAdapter(senderConfig())} {
		require.NoError(t, a.Capabilities().Validate())
	}
}

// A proactive message naming a template goes out as that approved template, with its value made
// one short line, and the receipt carries the message SID for a later report.
func TestSend_TemplateInSenderMode(t *testing.T) {
	f := &fakeMessages{}
	a := adapterOn(t, senderConfig(), f)
	rcpt, err := a.Send(context.Background(), channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{
		Text:      "🔔 *Reminder:*\n\nCall the dentist",
		Proactive: &channel.Proactive{Category: "reminder", TemplateKey: TemplateReminder, TemplateValues: []string{"Call the **dentist**\nabout Noa"}},
	})
	require.NoError(t, err)
	assert.True(t, rcpt.Accepted)
	require.Len(t, rcpt.Refs, 1)
	require.Len(t, f.forms, 1)
	form := f.forms[0]
	assert.Equal(t, testReminderSID, form.Get("ContentSid"))
	assert.JSONEq(t, `{"1":"Call the dentist about Noa"}`, form.Get("ContentVariables"))
	assert.Empty(t, form.Get("Body"), "a template carries no free text")
	assert.Equal(t, "whatsapp:+12025550101", form.Get("To"))
}

// The sandbox has no templates of MyPA's: asking for one is a programming error, never a send.
func TestSend_NoTemplateInSandbox(t *testing.T) {
	f := &fakeMessages{}
	a := adapterOn(t, sandboxConfig(), f)
	_, err := a.Send(context.Background(), channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{
		Proactive: &channel.Proactive{TemplateKey: TemplateReminder, TemplateValues: []string{"x"}},
	})
	require.Error(t, err)
	assert.Empty(t, f.forms)
}

// A free-form send returns one SID per part, so a report on any part is matched.
func TestSend_TextReturnsTheSIDOfEachPart(t *testing.T) {
	f := &fakeMessages{}
	a := adapterOn(t, senderConfig(), f)
	rcpt, err := a.Send(context.Background(), channel.Address{Channel: Name, Person: "+12025550101"},
		channel.Outbound{Text: strings.Repeat("A paragraph of text.\n\n", 120)})
	require.NoError(t, err)
	assert.True(t, rcpt.Accepted)
	assert.Len(t, rcpt.Refs, len(f.forms))
	assert.Greater(t, len(f.forms), 1)
}

// Twilio's error codes become the channel layer's reasons; any other error stays an error.
func TestSend_TwilioErrorsBecomeRefusals(t *testing.T) {
	cases := []struct {
		code   string
		reason channel.RefusalReason
	}{
		{"63016", channel.WindowClosed},
		{"63015", channel.NotReachable},
		{"21610", channel.NotReachable},
		{"63018", channel.RateLimited},
		{"21617", channel.TooLarge},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			f := &fakeMessages{status: http.StatusBadRequest, answer: `{"code":` + c.code + `,"message":"refused","status":400}`}
			rcpt, err := adapterOn(t, senderConfig(), f).Send(context.Background(),
				channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{Text: "hi"})
			require.NoError(t, err)
			assert.False(t, rcpt.Accepted)
			assert.Equal(t, c.reason, rcpt.Reason)
			if c.reason == channel.RateLimited {
				assert.False(t, rcpt.RetryAt.IsZero())
			}
		})
	}
	f := &fakeMessages{status: http.StatusInternalServerError, answer: `{"code":20500,"message":"internal"}`}
	_, err := adapterOn(t, senderConfig(), f).Send(context.Background(),
		channel.Address{Channel: Name, Person: "+12025550101"}, channel.Outbound{Text: "hi"})
	assert.Error(t, err, "a fault on Twilio's side is not a refusal: the policy retries it")
}

// A failed or undelivered report is published as a channel.Report for the delivery policy
// (V4-33), with Twilio's code read as a reason; a successful status publishes nothing.
func TestStatusCallback_FailedDeliveryIsPublishedAsAReport(t *testing.T) {
	cases := map[string]channel.RefusalReason{
		"63016": channel.WindowClosed,
		"63015": channel.NotReachable,
		"30008": channel.NotReachable, // unknown: the message still did not arrive
	}
	for code, reason := range cases {
		t.Run(code, func(t *testing.T) {
			_ = captureStatusLogs(t)
			pub := &mockPublisher{}
			form := failedForm(code)
			rr := httptest.NewRecorder()
			NewHandler(pub, "tok").HandleStatusCallback(rr, statusRequest(form, signForm("tok", statusURL, form)))
			assert.Equal(t, http.StatusOK, rr.Code)
			require.Len(t, pub.Published, 1)
			in, ok := pub.Published[0].(channel.Inbound)
			require.True(t, ok)
			assert.Equal(t, Name, in.Channel)
			assert.Equal(t, "+15550001111", in.Sender, "the person the message was for")
			require.NotNil(t, in.Report)
			assert.Equal(t, channel.Report{Ref: "SM123", Reason: reason}, *in.Report)
			assert.Empty(t, in.Text)
		})
	}

	pub := &mockPublisher{}
	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}, "To": {"whatsapp:+15550001111"}}
	NewHandler(pub, "tok").HandleStatusCallback(httptest.NewRecorder(), statusRequest(form, signForm("tok", statusURL, form)))
	assert.Empty(t, pub.Published)
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, any) error { return assert.AnError }

// A report that cannot be queued is answered with an error, so Twilio sends it again.
func TestStatusCallback_ReportThatCannotBeQueuedIsRetriedByTwilio(t *testing.T) {
	_ = captureStatusLogs(t)
	form := failedForm("63016")
	rr := httptest.NewRecorder()
	NewHandler(failingPublisher{}, "tok").HandleStatusCallback(rr, statusRequest(form, signForm("tok", statusURL, form)))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestTemplateValue(t *testing.T) {
	assert.Equal(t, "Call the dentist about Noa", templateValue("  Call the **dentist**\n\tabout _Noa_ "))
	long := templateValue(strings.Repeat("word ", 40))
	assert.LessOrEqual(t, len([]rune(long)), maxTemplateValue)
	assert.True(t, strings.HasSuffix(long, "…"))
}
