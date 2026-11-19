package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nivik/mypa/internal/channel"
)

// mediaAttachment is binary media handed to the multimodal LLM call (image or PDF).
type mediaAttachment struct {
	Data     []byte
	MimeType string
}

// isTextDocument reports whether a document's content type can be read as text and inlined.
func isTextDocument(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case contentType == "application/json", contentType == "application/xml", contentType == "application/csv",
		contentType == "application/x-yaml", contentType == "text/yaml":
		return true
	}
	return false
}

// ingestMedia normalizes an inbound attachment (P4-1): images, voice notes, PDFs and text
// documents, on whichever channel they arrived; the adapter fetches the file (V4-31). It may
// rewrite msg.Text (voice transcription, inlined document text) and marks the turn untrusted for
// attacker-controllable media (P3-1), under the label the adapter gave the attachment.
// stop=true ends the turn with err (nil after an "unsupported format" notice to the user).
func (e *Engine) ingestMedia(ctx context.Context, msg *channel.Inbound, secState *TurnSecurityState) (media mediaAttachment, stop bool, err error) {
	if len(msg.Attachments) == 0 {
		return media, false, nil
	}
	// One attachment per turn, as before: the first one the adapter reported.
	ref := msg.Attachments[0]
	contentType := strings.ToLower(ref.ContentType)
	slog.Info("inbound attachment received", "channel", msg.Channel, "kind", ref.Kind, "content_type", contentType)

	fetch := func(failure string) ([]byte, error) {
		if e.channels == nil {
			return nil, fmt.Errorf("no channels configured: cannot fetch from %q", msg.Channel)
		}
		data, err := e.channels.FetchAll(ctx, msg.Channel, ref)
		if err != nil {
			_ = e.sendMessage(ctx, *msg, failure)
			return nil, err
		}
		return data, nil
	}

	switch {
	case ref.Kind == channel.KindImage:
		data, err := fetch("❌ Failed to download photo.")
		if err != nil {
			return media, true, fmt.Errorf("failed to download photo: %w", err)
		}
		media.Data, media.MimeType = data, ref.ContentType
		// P3-1: images can carry instructions (screenshots, flyers, QR text).
		secState.MarkUntrusted(ref.Label)

	case ref.Kind == channel.KindAudio:
		audioData, err := fetch("❌ Failed to download voice message.")
		if err != nil {
			return media, true, fmt.Errorf("failed to download audio: %w", err)
		}
		transcribedText, err := e.audioClient.TranscribeAudio(ctx, audioData, "voice.ogg")
		if err != nil {
			_ = e.sendMessage(ctx, *msg, "❌ Failed to transcribe audio.")
			return media, true, fmt.Errorf("failed to transcribe: %w", err)
		}
		// Notify user of transcription
		_ = e.sendMessage(ctx, *msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))
		msg.Text = transcribedText

	case ref.Kind == channel.KindDocument && isTextDocument(contentType):
		docData, err := fetch("❌ Failed to download document.")
		if err != nil {
			return media, true, fmt.Errorf("failed to download document: %w", err)
		}
		docText := WrapUntrustedContent(ref.Label, ref.ID, string(docData))
		secState.MarkUntrusted(ref.Label + ":" + ref.ID)
		if strings.TrimSpace(msg.Text) != "" {
			msg.Text = fmt.Sprintf("%s\n\n[Attached Document Content]:\n%s", strings.TrimSpace(msg.Text), docText)
		} else {
			msg.Text = docText
		}

	case ref.Kind == channel.KindDocument && contentType == "application/pdf":
		data, err := fetch("❌ Failed to download PDF.")
		if err != nil {
			return media, true, fmt.Errorf("failed to download pdf: %w", err)
		}
		media.Data, media.MimeType = data, "application/pdf"
		secState.MarkUntrusted(ref.Label) // P3-1
		if strings.TrimSpace(msg.Text) == "" {
			msg.Text = "Please process and analyze the attached document."
		}

	default:
		_ = e.sendMessage(ctx, *msg, fmt.Sprintf("⚠️ Unsupported attachment format (%s). Please send a text file (.txt, .md, .csv), PDF, image, or voice note.", ref.ContentType))
		return media, true, nil
	}
	return media, false, nil
}
