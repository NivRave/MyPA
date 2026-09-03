package gmail

import (
	"context"
	b64 "encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
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
	endpoint    string // used for testing
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
	opts := []option.ClientOption{option.WithTokenSource(client)}
	if c.endpoint != "" {
		opts = append(opts, option.WithEndpoint(c.endpoint))
	}
	return gmailapi.NewService(ctx, opts...)
}

// EmailSummary represents a summarized email.
type EmailSummary struct {
	ID      string
	From    string
	Subject string
	Snippet string
}

// SearchEmails fetches up to a certain number of emails based on a query.
func (c *Client) SearchEmails(ctx context.Context, userID string, query string, maxResults int64) ([]EmailSummary, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}

	res, err := srv.Users.Messages.List("me").Q(query).MaxResults(maxResults).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list emails: %w", err)
	}

	var emails []EmailSummary
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

		emails = append(emails, EmailSummary{
			ID:      msg.Id,
			From:    from,
			Subject: subject,
			Snippet: msg.Snippet,
		})
	}

	return emails, nil
}

// ReadEmail fetches and decodes the full body of an email.
func (c *Client) ReadEmail(ctx context.Context, userID, messageID string) (string, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return "", err
	}

	msg, err := srv.Users.Messages.Get("me", messageID).Format("full").Do()
	if err != nil {
		return "", fmt.Errorf("failed to get email: %w", err)
	}

	body := decodeBody(msg.Payload)
	if body == "" {
		body = msg.Snippet // Fallback to snippet if body decoding fails
	}
	
	var from, date, subject, listUnsub string
	for _, header := range msg.Payload.Headers {
		if strings.EqualFold(header.Name, "From") {
			from = header.Value
		} else if strings.EqualFold(header.Name, "Date") {
			date = header.Value
		} else if strings.EqualFold(header.Name, "Subject") {
			subject = header.Value
		} else if strings.EqualFold(header.Name, "List-Unsubscribe") {
			listUnsub = header.Value
		}
	}
	
	headerText := fmt.Sprintf("From: %s\nDate: %s\nSubject: %s\n", from, date, subject)
	if listUnsub != "" {
		headerText += fmt.Sprintf("List-Unsubscribe: %s\n", listUnsub)
	}
	
	return headerText + "\n" + body, nil
}

// decodeBody recursively extracts the plain text from the message parts.
func decodeBody(part *gmailapi.MessagePart) string {
	if part.Body != nil && part.Body.Data != "" {
		data, err := b64.URLEncoding.DecodeString(part.Body.Data)
		if err == nil {
			// Prefer text/plain if we just look at the top level
			if part.MimeType == "text/plain" || part.MimeType == "text/html" {
				return string(data)
			}
		}
	}

	var textBody string
	var htmlBody string
	for _, subPart := range part.Parts {
		decoded := decodeBody(subPart)
		if subPart.MimeType == "text/plain" {
			textBody += decoded
		} else if subPart.MimeType == "text/html" {
			htmlBody += decoded
		} else if strings.HasPrefix(subPart.MimeType, "multipart/") {
			textBody += decoded
		}
	}

	if textBody != "" {
		return textBody
	}
	return htmlBody
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

// ArchiveEmail removes the INBOX label from an email.
func (c *Client) ArchiveEmail(ctx context.Context, userID, messageID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}
	req := &gmailapi.ModifyMessageRequest{
		RemoveLabelIds: []string{"INBOX"},
	}
	_, err = srv.Users.Messages.Modify("me", messageID, req).Do()
	return err
}

// SoftDeleteEmail moves an email to the Trash.
func (c *Client) SoftDeleteEmail(ctx context.Context, userID, messageID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}
	_, err = srv.Users.Messages.Trash("me", messageID).Do()
	return err
}

// ListLabels returns a map of label names to their IDs.
func (c *Client) ListLabels(ctx context.Context, userID string) (map[string]string, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return nil, err
	}
	res, err := srv.Users.Labels.List("me").Do()
	if err != nil {
		return nil, err
	}
	labels := make(map[string]string)
	for _, l := range res.Labels {
		labels[l.Name] = l.Id
	}
	return labels, nil
}

// ApplyLabel applies a specific label to an email.
func (c *Client) ApplyLabel(ctx context.Context, userID, messageID, labelID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}
	req := &gmailapi.ModifyMessageRequest{
		AddLabelIds: []string{labelID},
	}
	_, err = srv.Users.Messages.Modify("me", messageID, req).Do()
	return err
}

// CreateLabel creates a new Gmail label and returns its ID.
func (c *Client) CreateLabel(ctx context.Context, userID, labelName string) (string, error) {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return "", err
	}
	label := &gmailapi.Label{
		Name: labelName,
	}
	res, err := srv.Users.Labels.Create("me", label).Do()
	if err != nil {
		return "", err
	}
	return res.Id, nil
}

// UnsubscribeEmail parses the List-Unsubscribe header and attempts to unsubscribe.
func (c *Client) UnsubscribeEmail(ctx context.Context, userID, messageID string) error {
	srv, err := c.getService(ctx, userID)
	if err != nil {
		return err
	}

	msg, err := srv.Users.Messages.Get("me", messageID).Format("metadata").Do()
	if err != nil {
		return fmt.Errorf("failed to fetch email: %w", err)
	}

	var listUnsub string
	for _, header := range msg.Payload.Headers {
		if strings.EqualFold(header.Name, "List-Unsubscribe") {
			listUnsub = header.Value
			break
		}
	}

	if listUnsub == "" {
		return fmt.Errorf("no List-Unsubscribe header found in this email")
	}

	// Find HTTPS or HTTP link
	httpRe := regexp.MustCompile(`<(https?://[^>]+)>`)
	if match := httpRe.FindStringSubmatch(listUnsub); len(match) > 1 {
		url := match[1]
		
		req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		
		req, _ = http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err = client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
	}

	// Find mailto link
	mailtoRe := regexp.MustCompile(`<mailto:([^>]+)>`)
	if match := mailtoRe.FindStringSubmatch(listUnsub); len(match) > 1 {
		mailtoURL := match[1]
		
		parts := strings.SplitN(mailtoURL, "?", 2)
		to := parts[0]
		subject := "Unsubscribe"
		
		if len(parts) > 1 {
			if strings.Contains(parts[1], "subject=") {
				subjectMatches := regexp.MustCompile(`subject=([^&]+)`).FindStringSubmatch(parts[1])
				if len(subjectMatches) > 1 {
					subject = subjectMatches[1]
				}
			}
		}

		rawMessage := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\nUnsubscribe", to, subject)
		msgReq := &gmailapi.Message{
			Raw: b64.URLEncoding.EncodeToString([]byte(rawMessage)),
		}

		_, err = srv.Users.Messages.Send("me", msgReq).Do()
		if err != nil {
			return fmt.Errorf("failed to send unsubscribe email: %w", err)
		}
		
		return nil
	}

	return fmt.Errorf("could not parse a valid unsubscribe link from: %s", listUnsub)
}
