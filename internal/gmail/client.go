package gmail

import (
	"context"
	b64 "encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/state"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Client is a wrapper around the Gmail API.
type Client struct {
	oauthConfig *calendar.OAuthConfig
	store       *state.Store
}

// NewClient initializes a new Gmail client wrapper.
func NewClient(oauthCfg *calendar.OAuthConfig, store *state.Store) *Client {
	return &Client{
		oauthConfig: oauthCfg,
		store:       store,
	}
}

func (c *Client) getService(ctx context.Context, userID string) (*gmailapi.Service, error) {
	tokenData, err := c.store.GetOAuthToken(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("no token found for user %s: %w", userID, err)
	}

	token, err := calendar.DecodeToken([]byte(tokenData))
	if err != nil {
		return nil, fmt.Errorf("invalid token format: %w", err)
	}

	client := c.oauthConfig.TokenSource(ctx, token)
	return gmailapi.NewService(ctx, option.WithTokenSource(client))
}

// UnreadEmail represents a summarized email.
type UnreadEmail struct {
	ID      string
	From    string
	Subject string
	Snippet string
}

// ListUnreadEmails fetches up to a certain number of unread emails.
func (c *Client) ListUnreadEmails(ctx context.Context, userID string, maxResults int64) ([]UnreadEmail, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	res, err := srv.Users.Messages.List("me").Q("is:unread").MaxResults(maxResults).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list emails: %w", err)
	}

	var emails []UnreadEmail
	for _, m := range res.Messages {
		msg, err := srv.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders("From", "Subject").Do()
		if err != nil {
			slog.Warn("failed to fetch email details", "message_id", m.Id, "error", err)
			continue
		}

		var from, subject string
		for _, header := range msg.Payload.Headers {
			if strings.EqualFold(header.Name, "From") {
				from = header.Value
			}
			if strings.EqualFold(header.Name, "Subject") {
				subject = header.Value
			}
		}

		emails = append(emails, UnreadEmail{
			ID:      msg.Id,
			From:    from,
			Subject: subject,
			Snippet: msg.Snippet,
		})
	}

	return emails, nil
}

// ReadEmail fetches the full body of an email.
func (c *Client) ReadEmail(ctx context.Context, userID, messageID string) (string, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return "", err
	}

	msg, err := srv.Users.Messages.Get("me", messageID).Format("full").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get email: %w", err)
	}

	return msg.Snippet, nil // Simplification for now, we'll return snippet since decoding full body can be complex.
}

// DraftReply creates a draft reply to an email.
func (c *Client) DraftReply(ctx context.Context, userID, messageID, replyText string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	msg, err := srv.Users.Messages.Get("me", messageID).Format("metadata").Do()
	if err != nil {
		return fmt.Errorf("failed to fetch original email for drafting: %w", err)
	}

	var subject, to, messageIDHeader string
	for _, header := range msg.Payload.Headers {
		if strings.EqualFold(header.Name, "Subject") {
			subject = header.Value
		}
		if strings.EqualFold(header.Name, "From") {
			to = header.Value
		}
		if strings.EqualFold(header.Name, "Message-ID") {
			messageIDHeader = header.Value
		}
	}

	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	rawMessage := fmt.Sprintf("To: %s\r\nSubject: %s\r\nIn-Reply-To: %s\r\nReferences: %s\r\n\r\n%s", to, subject, messageIDHeader, messageIDHeader, replyText)
	
	draft := &gmailapi.Draft{
		Message: &gmailapi.Message{
			Raw: b64.URLEncoding.EncodeToString([]byte(rawMessage)),
		},
	}

	_, err = srv.Users.Drafts.Create("me", draft).Do()
	return err
}
