package twilio

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/logging"
)

// EventPublisher receives each normalised message (a channel.Inbound). In the app it is the
// adapter's sink, which publishes to the inbound queue.
type EventPublisher interface {
	Publish(ctx context.Context, v any) error
}

// Handler handles incoming Twilio Webhooks.
type Handler struct {
	publisher EventPublisher
	authToken string
	publicURL string
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

// WithPublicURL makes the handler verify signatures against the install's public address
// (PUBLIC_URL, V4-40) instead of rebuilding it from the request and its forwarded headers. Twilio
// signs the address it calls, which is the public one; an empty base keeps the request's own.
func (h *Handler) WithPublicURL(base string) *Handler {
	h.publicURL = strings.TrimSuffix(base, "/")
	return h
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

	// The number without the "whatsapp:" prefix is the person's ID; a reply is sent to it.
	userID := strings.TrimPrefix(from, "whatsapp:")

	// Forwarded regardless of authorization: the worker handles unauthorized pairing flows.
	msg := channel.Inbound{
		Channel:   Name,
		MessageID: messageID,
		Sender:    userID,
		// WhatsApp over Twilio has direct chats only (DE-8); replies go to the number, so the
		// conversation has no ID of its own.
		Conversation: channel.Conversation{Kind: channel.Direct, Addressed: true},
		Text:         body,
	}
	if mediaURL != "" {
		msg.Attachments = []channel.AttachmentRef{MediaRef(mediaURL, mediaContentType)}
	}

	// P3-3: never log message content at INFO (length + message_id only).
	logging.LogMessageText(r.Context(), nil, "received twilio message", msg.Text,
		"user_id", msg.Sender, "message_id", msg.MessageID, "has_media", mediaURL != "", "content_type", mediaContentType)

	err = h.publisher.Publish(context.Background(), msg)
	if err != nil {
		slog.Error("failed to publish twilio message", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Labels for the untrusted-content marks of attachments (P3-1). They appear in confirmation
// prompts and are pinned by the conformance transcripts.
const (
	imageLabel    = "whatsapp_image"
	audioLabel    = "whatsapp_audio"
	pdfLabel      = "whatsapp_pdf"
	documentLabel = "whatsapp_document"
)

// MediaRef classifies the first media item of a message. The media URL is the reference the
// adapter fetches later. Exported so tests that build inbound messages by hand classify media
// exactly as the webhook does.
func MediaRef(mediaURL, contentType string) channel.AttachmentRef {
	ref := channel.AttachmentRef{ID: mediaURL, ContentType: contentType}
	switch ct := strings.ToLower(contentType); {
	case strings.HasPrefix(ct, "image/"):
		ref.Kind, ref.Label = channel.KindImage, imageLabel
	case strings.HasPrefix(ct, "audio/"), ct == "application/ogg":
		ref.Kind, ref.Label = channel.KindAudio, audioLabel
	case ct == "application/pdf":
		ref.Kind, ref.Label = channel.KindDocument, pdfLabel
	default:
		ref.Kind, ref.Label = channel.KindDocument, documentLabel
	}
	return ref
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
// undelivered messages are logged at WARN with the error code and a hint, and published as a
// channel.Report so the delivery policy can try another route (V4-33); other statuses are logged
// at DEBUG. It never logs message content.
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
		sid := r.PostForm.Get("MessageSid")
		if sid == "" {
			break
		}
		report := channel.Inbound{
			Channel:   Name,
			MessageID: "report-" + sid + "-" + status,
			Sender:    strings.TrimPrefix(r.PostForm.Get("To"), "whatsapp:"),
			Report:    &channel.Report{Ref: sid, Reason: reportReason(code)},
		}
		if err := h.publisher.Publish(r.Context(), report); err != nil {
			// Twilio retries a callback that fails, so the report is not lost.
			slog.Error("failed to publish a whatsapp delivery report", "message_sid", sid, "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	default:
		slog.Debug("whatsapp delivery status", attrs...)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) validateSignature(r *http.Request, form url.Values, signature string) bool {
	fullURL := h.signedURL(r)

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

// signedURL is the address Twilio signed: the public address when it is known, otherwise the one
// the request reached, as the proxy in front reports it.
func (h *Handler) signedURL(r *http.Request) string {
	if h.publicURL != "" {
		return h.publicURL + r.URL.RequestURI()
	}
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
	return scheme + "://" + host + r.URL.RequestURI()
}
