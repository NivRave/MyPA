package twilio

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nivik/mypa/internal/broker"
	"github.com/nivik/mypa/internal/models"
)

// Handler handles incoming Twilio Webhooks.
type Handler struct {
	publisher *broker.Publisher
}

// NewHandler creates a new Twilio Webhook handler.
func NewHandler(publisher *broker.Publisher) *Handler {
	return &Handler{
		publisher: publisher,
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

	from := r.FormValue("From")
	body := r.FormValue("Body")
	mediaURL := r.FormValue("MediaUrl0")
	messageID := r.FormValue("MessageSid")

	// Strip "whatsapp:" prefix if present for uniform UserID storage, or keep it?
	// It's probably best to keep it so we can just send it back out exactly as is.
	userID := from 
	if strings.HasPrefix(userID, "whatsapp:") {
		userID = strings.TrimPrefix(userID, "whatsapp:")
	}

	msg := models.Message{
		ID:        messageID,
		UserID:    userID,
		Text:      body,
		Source:    "whatsapp",
		MediaURL:  mediaURL,
	}

	slog.Info("received twilio message", "user_id", msg.UserID, "text", msg.Text, "has_media", mediaURL != "")

	err = h.publisher.Publish(context.Background(), msg)
	if err != nil {
		slog.Error("failed to publish twilio message", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
