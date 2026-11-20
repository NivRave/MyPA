package twilio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeContentAPI answers as Twilio's Content API does and records every call.
type fakeContentAPI struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []string
	created  map[string]map[string]any // SID -> create request
	approval map[string]string         // SID -> status
	failOn   string                    // a path prefix that answers 400
}

func newFakeContentAPI(t *testing.T) *fakeContentAPI {
	return &fakeContentAPI{t: t, created: map[string]map[string]any{}, approval: map[string]string{}}
}

func (f *fakeContentAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req.Method+" "+req.URL.Host+req.URL.Path)
	answer := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
	}
	if req.URL.Scheme != "https" || req.URL.Host != "content.twilio.com" {
		f.t.Errorf("unexpected request to %s", req.URL)
		return answer(http.StatusBadGateway, "")
	}
	if user, pass, ok := req.BasicAuth(); !ok || user != "AC1" || pass != "tok" {
		return answer(http.StatusUnauthorized, `{"code":20003,"message":"Authenticate"}`)
	}
	if f.failOn != "" && strings.HasPrefix(req.URL.Path, f.failOn) {
		return answer(http.StatusBadRequest, `{"code":20001,"message":"Template name already exists"}`)
	}
	path := req.URL.Path
	switch {
	case req.Method == http.MethodPost && path == "/v1/Content":
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(req.Body).Decode(&body))
		sid := fmt.Sprintf("HX%032x", len(f.created)+1)
		f.created[sid] = body
		f.approval[sid] = "unsubmitted"
		return answer(http.StatusCreated, `{"sid":"`+sid+`"}`)
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/ApprovalRequests/whatsapp"):
		sid := strings.Split(path, "/")[3]
		var body map[string]string
		require.NoError(f.t, json.NewDecoder(req.Body).Decode(&body))
		assert.Equal(f.t, "UTILITY", body["category"])
		f.approval[sid] = "received"
		return answer(http.StatusCreated, `{"name":"`+body["name"]+`","category":"UTILITY","status":"received"}`)
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/ApprovalRequests"):
		sid := strings.Split(path, "/")[3]
		status, ok := f.approval[sid]
		if !ok {
			return answer(http.StatusNotFound, `{"code":20404,"message":"The requested resource was not found"}`)
		}
		return answer(http.StatusOK, `{"sid":"`+sid+`","whatsapp":{"status":"`+status+`","rejection_reason":""}}`)
	}
	f.t.Errorf("unexpected Content API call %s %s", req.Method, path)
	return answer(http.StatusNotFound, "")
}

func contentClient(f *fakeContentAPI, cfg Config) *Client {
	return NewClient(cfg, WithHTTPClient(&http.Client{Transport: f}))
}

var senderCfg = Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: "whatsapp:+12025550199"}

func TestGuide_SenderSubmitsMissingTemplatesAndPrintsTheSettings(t *testing.T) {
	f := newFakeContentAPI(t)
	var out bytes.Buffer
	require.NoError(t, Guide(context.Background(), senderCfg, contentClient(f, senderCfg), &out, true))

	require.Len(t, f.created, 2, "both templates are created in the owner's account")
	for sid, req := range f.created {
		tp, ok := templateByKey(map[string]string{"mypa_reminder": TemplateReminder, "mypa_briefing": TemplateBriefing}[req["friendly_name"].(string)])
		require.True(t, ok)
		assert.Equal(t, tp.Body, req["types"].(map[string]any)["twilio/text"].(map[string]any)["body"], "the wording sent is MyPA's")
		assert.Equal(t, tp.Samples[0], req["variables"].(map[string]any)["1"])
		assert.Equal(t, "received", f.approval[sid], "and submitted for WhatsApp's approval")
		assert.Contains(t, out.String(), templateEnvKeys[tp.Key]+"="+sid)
	}
	// The SID it printed is the one /doctor then asks about.
	sid := strings.TrimPrefix(lineWith(t, out.String(), "TWILIO_TEMPLATE_REMINDER_SID="), "TWILIO_TEMPLATE_REMINDER_SID=")
	a, err := contentClient(f, senderCfg).TemplateApproval(context.Background(), sid)
	require.NoError(t, err)
	assert.Equal(t, "received", a.Status)
}

func TestGuide_SenderWithoutSubmitChangesNothing(t *testing.T) {
	f := newFakeContentAPI(t)
	var out bytes.Buffer
	cfg := senderCfg
	cfg.TemplateSIDs = map[string]string{TemplateReminder: "HX0123456789abcdef0123456789abcdef"}
	require.NoError(t, Guide(context.Background(), cfg, contentClient(f, cfg), &out, false))
	assert.Empty(t, f.calls, "nothing is sent to Twilio without --submit")
	for _, tp := range Templates() {
		assert.Contains(t, out.String(), tp.Body, "the guide shows the exact wording")
	}
	assert.Contains(t, out.String(), "--submit")
	assert.Contains(t, out.String(), "set (HX0123456789abcdef0123456789abcdef)")

	f.calls = nil
	out.Reset()
	require.NoError(t, Guide(context.Background(), cfg, contentClient(f, cfg), &out, true))
	assert.Len(t, f.created, 1, "a template already set is not created again")
}

func TestGuide_StopsAtAFailedSubmission(t *testing.T) {
	f := newFakeContentAPI(t)
	f.failOn = "/v1/Content/HX"
	err := Guide(context.Background(), senderCfg, contentClient(f, senderCfg), io.Discard, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not submitted")
	assert.Contains(t, err.Error(), "Template name already exists", "Twilio's own reason is passed on")
	assert.Contains(t, err.Error(), "TWILIO_TEMPLATE_REMINDER_SID=HX", "the owner learns the SID that was created")
}

func TestGuide_Sandbox(t *testing.T) {
	cfg := Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: SandboxSender, SandboxJoin: "join plenty-tiger"}
	var out bytes.Buffer
	require.NoError(t, Guide(context.Background(), cfg, nil, &out, false))
	assert.Contains(t, out.String(), "`join plenty-tiger`")
	assert.Contains(t, out.String(), "TWILIO_WHATSAPP_MODE=sender")
	assert.Contains(t, out.String(), "72 hours")

	err := Guide(context.Background(), cfg, nil, io.Discard, true)
	require.Error(t, err, "a sandbox has no templates to submit")
	assert.Contains(t, err.Error(), "registered sender")

	assert.Error(t, Guide(context.Background(), Config{}, nil, io.Discard, false), "WhatsApp is not configured")
}

func TestTemplateApproval_RefusesWhatIsNotAContentSID(t *testing.T) {
	f := newFakeContentAPI(t)
	_, err := contentClient(f, senderCfg).TemplateApproval(context.Background(), "HX/../../Accounts")
	require.Error(t, err)
	assert.Empty(t, f.calls, "a malformed SID never becomes a request path")

	_, err = contentClient(f, senderCfg).TemplateApproval(context.Background(), "HX0123456789abcdef0123456789abcdef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func lineWith(t *testing.T, text, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(l)
		}
	}
	t.Fatalf("no line starting with %q in:\n%s", prefix, text)
	return ""
}
