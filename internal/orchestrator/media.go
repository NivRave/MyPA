package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nivik/mypa/internal/models"
)

// mediaAttachment is binary media handed to the multimodal LLM call (image or PDF).
type mediaAttachment struct {
	Data     []byte
	MimeType string
}

// ingestMedia normalizes an inbound attachment (P4-1): Telegram photos and voice notes, WhatsApp
// images, PDFs, text documents and voice notes. It may rewrite msg.Text (voice transcription,
// inlined document text) and marks the turn untrusted for attacker-controllable media (P3-1).
// stop=true ends the turn with err (nil after an "unsupported format" notice to the user).
func (e *Engine) ingestMedia(ctx context.Context, msg *models.Message, secState *TurnSecurityState) (media mediaAttachment, stop bool, err error) {
	// 0. Intercept and transcribe Voice Messages and Photos
	if msg.PhotoFileID != "" {
		filePath, err := e.tgClient.GetFile(ctx, msg.PhotoFileID)
		if err != nil {
			_ = e.sendMessage(ctx, *msg, "❌ Failed to retrieve photo metadata.")
			return media, true, fmt.Errorf("failed to get photo path: %w", err)
		}
		media.Data, err = e.tgClient.DownloadFile(ctx, filePath)
		if err != nil {
			_ = e.sendMessage(ctx, *msg, "❌ Failed to download photo.")
			return media, true, fmt.Errorf("failed to download photo: %w", err)
		}
		media.MimeType = "image/jpeg"
		// P3-1: images can carry instructions (screenshots, flyers, QR text).
		secState.MarkUntrusted("telegram_photo")
	} else if msg.VoiceFileID != "" {
		filePath, err := e.tgClient.GetFile(ctx, msg.VoiceFileID)
		if err != nil {
			_ = e.sendMessage(ctx, *msg, "❌ Failed to retrieve voice message metadata.")
			return media, true, fmt.Errorf("failed to get file path: %w", err)
		}

		audioData, err := e.tgClient.DownloadFile(ctx, filePath)
		if err != nil {
			_ = e.sendMessage(ctx, *msg, "❌ Failed to download voice message.")
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
	} else if msg.Source == "whatsapp" && msg.MediaURL != "" {
		mediaType := strings.ToLower(msg.MediaContentType)
		slog.Info("WhatsApp media received", "media_url", msg.MediaURL, "content_type", mediaType)

		isTextDoc := strings.HasPrefix(mediaType, "text/") ||
			mediaType == "application/json" ||
			mediaType == "application/xml" ||
			mediaType == "application/csv" ||
			mediaType == "application/x-yaml" ||
			mediaType == "text/yaml"

		if strings.HasPrefix(mediaType, "image/") {
			media.Data, err = e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, *msg, "❌ Failed to download WhatsApp photo.")
				return media, true, fmt.Errorf("failed to download twilio photo: %w", err)
			}
			media.MimeType = msg.MediaContentType
			secState.MarkUntrusted("whatsapp_image") // P3-1
		} else if isTextDoc {
			docData, err := e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, *msg, "❌ Failed to download WhatsApp document.")
				return media, true, fmt.Errorf("failed to download twilio document: %w", err)
			}
			docText := WrapUntrustedContent("whatsapp_document", msg.MediaURL, string(docData))
			secState.MarkUntrusted("whatsapp_document:" + msg.MediaURL)
			if strings.TrimSpace(msg.Text) != "" {
				msg.Text = fmt.Sprintf("%s\n\n[Attached Document Content]:\n%s", strings.TrimSpace(msg.Text), docText)
			} else {
				msg.Text = docText
			}
		} else if mediaType == "application/pdf" {
			media.Data, err = e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, *msg, "❌ Failed to download WhatsApp PDF.")
				return media, true, fmt.Errorf("failed to download twilio pdf: %w", err)
			}
			media.MimeType = "application/pdf"
			secState.MarkUntrusted("whatsapp_pdf") // P3-1
			if strings.TrimSpace(msg.Text) == "" {
				msg.Text = "Please process and analyze the attached document."
			}
		} else if strings.HasPrefix(mediaType, "audio/") || mediaType == "application/ogg" {
			// Handle Twilio Voice Messages
			audioData, err := e.twClient.DownloadMedia(ctx, msg.MediaURL)
			if err != nil {
				_ = e.sendMessage(ctx, *msg, "❌ Failed to download WhatsApp voice message.")
				return media, true, fmt.Errorf("failed to download twilio media: %w", err)
			}

			transcribedText, err := e.audioClient.TranscribeAudio(ctx, audioData, "voice.ogg")
			if err != nil {
				_ = e.sendMessage(ctx, *msg, "❌ Failed to transcribe WhatsApp audio.")
				return media, true, fmt.Errorf("failed to transcribe twilio media: %w", err)
			}

			_ = e.sendMessage(ctx, *msg, fmt.Sprintf("🗣️ *Transcribed:* %s", transcribedText))
			msg.Text = transcribedText
		} else {
			_ = e.sendMessage(ctx, *msg, fmt.Sprintf("⚠️ Unsupported attachment format (%s). Please send a text file (.txt, .md, .csv), PDF, image, or voice note.", msg.MediaContentType))
			return media, true, nil
		}
	}
	return media, false, nil
}
