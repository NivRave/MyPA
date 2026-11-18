package twilio

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"github.com/nivik/mypa/internal/logging"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/nivik/mypa/internal/models"
)

// EventPublisher handles publishing events to a broker.
type EventPublisher interface {
	Publish(ctx context.Context, v any) error
}

// Handler handles incoming Twilio Webhooks.
type Handler struct {
	publisher EventPublisher
	authToken string
}

// NewHandler creates a new Twilio Webhook handler.
func NewHandler(publisher EventPublisher, authToken ...string) *Handler {
	token := ""
	if len(authToken) > 0 {
		token = authToken[0]
	}
	return &Handler{
		publisher: publisher,
		authToken: token,
	}
}

// HandleWebhook processes the incoming Twilio webhook POST request.
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		slog.Error("failed to parse twilio form", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !h.authenticated(w, r) {
		return
	}

	from := r.FormValue("From")
	body := r.FormValue("Body")
	mediaURL := r.FormValue("MediaUrl0")
	mediaContentType := r.FormValue("MediaContentType0")
	messageID := r.FormValue("MessageSid")

	// Strip "whatsapp:" prefix if present for uniform UserID storage, or keep it?
	// It's probably best to keep it so we can just send it back out exactly as is.
	userID := strings.TrimPrefix(from, "whatsapp:")

	// Forward to orchestrator regardless of authorization
	// (Orchestrator handles unauthorized pairing flows)

	msg := models.Message{
		ID:               messageID,
		UserID:           userID,
		Text:             body,
		Source:           "whatsapp",
		MediaURL:         mediaURL,
		MediaContentType: mediaContentType,
	}

	// P3-3: never log message content at INFO (length + message_id only).
	logging.LogMessageText(r.Context(), nil, "received twilio message", msg.Text,
		"user_id", msg.UserID, "message_id", msg.ID, "has_media", mediaURL != "", "content_type", mediaContentType)

	err = h.publisher.Publish(context.Background(), msg)
	if err != nil {
		slog.Error("failed to publish twilio message", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// authenticated verifies the X-Twilio-Signature of a parsed request and writes 403 when it fails.
// It fails closed when no auth token is configured: an HMAC keyed with "" is computable by anyone.
func (h *Handler) authenticated(w http.ResponseWriter, r *http.Request) bool {
	if h.authToken == "" {
		slog.Error("twilio webhook rejected: TWILIO_AUTH_TOKEN is not configured", "path", r.URL.Path, "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden: invalid signature", http.StatusForbidden)
		return false
	}
	sig := r.Header.Get("X-Twilio-Signature")
	if sig == "" || !h.validateSignature(r, r.PostForm, sig) {
		slog.Warn("twilio webhook signature verification failed", "path", r.URL.Path, "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden: invalid signature", http.StatusForbidden)
		return false
	}
	return true
}

// deliveryErrorHints explains common WhatsApp delivery failures to the operator (BL-8).
var deliveryErrorHints = map[string]string{
	"63003": "WhatsApp could not find the recipient address",
	"63015": "recipient is not (or no longer) a sandbox participant: re-send the sandbox 'join <word>' message",
	"63016": "outside the 24-hour session window: free-form replies need a recent user message (or an approved template)",
	"63018": "rate limit exceeded for this sender",
	"63024": "invalid message recipient",
	"63038": "account daily message limit exceeded",
}

// HandleStatusCallback receives Twilio delivery reports for outbound messages (BL-8). Failed or
// undelivered messages are logged at WARN with the error code and a hint; other statuses at DEBUG.
// It never publishes anything and never logs message content.
func (h *Handler) HandleStatusCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if !h.authenticated(w, r) {
		return
	}

	status := r.PostForm.Get("MessageStatus")
	attrs := []any{
		"message_sid", r.PostForm.Get("MessageSid"),
		"status", status,
		"user_id", strings.TrimPrefix(r.PostForm.Get("To"), "whatsapp:"),
	}
	switch status {
	case "failed", "undelivered":
		code := r.PostForm.Get("ErrorCode")
		attrs = append(attrs, "error_code", code)
		if hint, ok := deliveryErrorHints[code]; ok {
			attrs = append(attrs, "hint", hint)
		}
		slog.Warn("whatsapp message not delivered", attrs...)
	default:
		slog.Debug("whatsapp delivery status", attrs...)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) validateSignature(r *http.Request, form url.Values, signature string) bool {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	fullURL := scheme + "://" + host + r.URL.RequestURI()

	var keys []string
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(fullURL)
	for _, k := range keys {
		sb.WriteString(k)
		vals := form[k]
		if len(vals) > 0 {
			sb.WriteString(vals[0])
		}
	}

	mac := hmac.New(sha1.New, []byte(h.authToken))
	mac.Write([]byte(sb.String()))
	expectedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSig)) == 1
}
