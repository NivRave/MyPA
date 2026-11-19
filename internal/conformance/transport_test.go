package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Credentials the fake platforms accept. They are checked on every request, so a client that
// stopped authenticating (or sent the wrong secret) fails the suite.
const (
	telegramBotToken = "conformance-bot-token"
	// telegramSecretToken is what Telegram sends with every update; the adapter rejects others.
	telegramSecretToken = "conformance-secret-token"
	twilioAccountSID    = "ACconformance"
	twilioAuthToken     = "conformance-auth-token"
	// twilioSender is the configured WhatsApp sender. A fictional number (reserved 555-01xx block).
	twilioSender = "whatsapp:+12025550199"
)

// wireMessage is one message as the platform API received it.
type wireMessage struct {
	Platform string // "telegram" or "whatsapp"
	To       string // Telegram chat_id, or the WhatsApp "To" address
	Meta     string // how it was sent: parse mode, sender
	Text     string // the text exactly as it was put on the wire
}

// fakePlatforms is the fake transport: an http.RoundTripper standing in for the Telegram Bot API
// and the Twilio REST API. It answers as they would, records every message sent to a user, and
// serves the files a scenario attached. Any request to another host fails the test.
type fakePlatforms struct {
	t *testing.T

	mu            sync.Mutex
	sent          []wireMessage
	telegramFiles map[string][]byte // Telegram file_id -> content
	twilioMedia   map[string][]byte // Twilio media URL path -> content
	downloads     []string          // what was fetched, in order
}

func newFakePlatforms(t *testing.T) *fakePlatforms {
	return &fakePlatforms{
		t:             t,
		telegramFiles: map[string][]byte{},
		twilioMedia:   map[string][]byte{},
	}
}

func (f *fakePlatforms) addTelegramFile(fileID string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.telegramFiles[fileID] = data
}

func (f *fakePlatforms) addTwilioMedia(urlPath string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.twilioMedia[urlPath] = data
}

// messages returns everything sent to users so far.
func (f *fakePlatforms) messages() []wireMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wireMessage(nil), f.sent...)
}

// fetched returns the downloads requested so far.
func (f *fakePlatforms) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.downloads...)
}

// RoundTrip implements http.RoundTripper.
func (f *fakePlatforms) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return f.refuse(req, "platform APIs are only called over https")
	}
	switch req.URL.Host {
	case "api.telegram.org":
		return f.telegram(req)
	case "api.twilio.com":
		return f.twilio(req)
	default:
		return f.refuse(req, "no platform API lives on this host")
	}
}

func (f *fakePlatforms) refuse(req *http.Request, why string) (*http.Response, error) {
	f.t.Errorf("unexpected outbound request %s %s://%s%s: %s", req.Method, req.URL.Scheme, req.URL.Host, req.URL.Path, why)
	return nil, fmt.Errorf("conformance transport refused %s: %s", req.URL.Host, why)
}

func (f *fakePlatforms) telegram(req *http.Request) (*http.Response, error) {
	botPrefix := "/bot" + telegramBotToken + "/"
	filePrefix := "/file/bot" + telegramBotToken + "/"
	path := req.URL.Path

	switch {
	case req.Method == http.MethodPost && path == botPrefix+"sendMessage":
		var body struct {
			ChatID    string `json:"chat_id"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return respond(req, http.StatusBadRequest, `{"ok":false,"description":"Bad Request: cannot parse body"}`), nil
		}
		meta := "parse_mode=" + body.ParseMode
		if body.ParseMode == "" {
			meta = "parse_mode=(none)"
		}
		f.record(wireMessage{Platform: "telegram", To: body.ChatID, Meta: meta, Text: body.Text})
		return respond(req, http.StatusOK, `{"ok":true,"result":{}}`), nil

	case req.Method == http.MethodGet && path == botPrefix+"getFile":
		fileID := req.URL.Query().Get("file_id")
		f.mu.Lock()
		_, known := f.telegramFiles[fileID]
		f.downloads = append(f.downloads, "telegram getFile "+fileID)
		f.mu.Unlock()
		if !known {
			return respond(req, http.StatusBadRequest, `{"ok":false,"description":"Bad Request: invalid file_id"}`), nil
		}
		return respond(req, http.StatusOK, fmt.Sprintf(`{"ok":true,"result":{"file_id":%q,"file_path":%q}}`, fileID, "files/"+fileID)), nil

	case req.Method == http.MethodGet && strings.HasPrefix(path, filePrefix+"files/"):
		fileID := strings.TrimPrefix(path, filePrefix+"files/")
		f.mu.Lock()
		data, known := f.telegramFiles[fileID]
		f.downloads = append(f.downloads, "telegram download "+fileID)
		f.mu.Unlock()
		if !known {
			return respond(req, http.StatusNotFound, "not found"), nil
		}
		return respondBytes(req, http.StatusOK, data), nil
	}
	if !strings.Contains(path, telegramBotToken) {
		return respond(req, http.StatusUnauthorized, `{"ok":false,"description":"Unauthorized"}`), nil
	}
	return f.refuse(req, "the suite does not expect this Telegram method")
}

func (f *fakePlatforms) twilio(req *http.Request) (*http.Response, error) {
	if user, pass, ok := req.BasicAuth(); !ok || user != twilioAccountSID || pass != twilioAuthToken {
		return respond(req, http.StatusUnauthorized, `{"code":20003,"message":"Authenticate"}`), nil
	}
	path := req.URL.Path

	switch {
	case req.Method == http.MethodPost && path == "/2010-04-01/Accounts/"+twilioAccountSID+"/Messages.json":
		if err := req.ParseForm(); err != nil {
			return respond(req, http.StatusBadRequest, `{"code":21602,"message":"bad form"}`), nil
		}
		f.record(wireMessage{
			Platform: "whatsapp",
			To:       req.PostForm.Get("To"),
			Meta:     "from=" + req.PostForm.Get("From"),
			Text:     req.PostForm.Get("Body"),
		})
		return respond(req, http.StatusCreated, `{"sid":"SMfake","status":"queued"}`), nil

	case req.Method == http.MethodGet && strings.Contains(path, "/Media/"):
		f.mu.Lock()
		data, known := f.twilioMedia[path]
		f.downloads = append(f.downloads, "twilio media "+path[strings.LastIndex(path, "/")+1:])
		f.mu.Unlock()
		if !known {
			return respond(req, http.StatusNotFound, `{"code":20404,"message":"not found"}`), nil
		}
		return respondBytes(req, http.StatusOK, data), nil
	}
	return f.refuse(req, "the suite does not expect this Twilio endpoint")
}

func (f *fakePlatforms) record(m wireMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
}

func respond(req *http.Request, status int, body string) *http.Response {
	return respondBytes(req, status, []byte(body))
}

func respondBytes(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}
}
