package twilio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nivik/mypa/internal/channel"
)

// Proactive delivery over WhatsApp (V4-33): the window and sandbox rule the adapter declares,
// sending an approved template, and reading Twilio's refusals as the channel layer's reasons.

// Window is how long after a person last wrote WhatsApp accepts a free-form message from the
// business. Outside it only an approved template goes through (Twilio error 63016).
const Window = 24 * time.Hour

// APIError is an answer outside 2xx from Twilio's Messages API, with Twilio's own error code.
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("twilio API returned status %d (error %d): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("twilio API returned status %d: %s", e.Status, e.Message)
}

// refusalFor maps a Twilio error code to the reason the channel layer uses. ok is false for a
// code that says nothing about the recipient (a fault of the request or of Twilio).
func refusalFor(code string) (channel.RefusalReason, bool) {
	switch code {
	case "63016": // outside the 24-hour window
		return channel.WindowClosed, true
	case "63015", // not (or no longer) a sandbox participant
		"63003", "63024", // not a WhatsApp user, invalid recipient
		"21211", "21614", "21610": // invalid number, not a mobile number, the person sent STOP
		return channel.NotReachable, true
	case "20429", "63018", "63038": // too many requests, sender rate limit, daily limit
		return channel.RateLimited, true
	case "21617": // body too long
		return channel.TooLarge, true
	}
	return "", false
}

// retryAfter is when to try again after a rate limit: the daily limit resets slowly.
func retryAfter(code string, now time.Time) time.Time {
	if code == "63038" {
		return now.Add(time.Hour)
	}
	return now.Add(time.Minute)
}

// receiptFor turns a send error into a refusal when Twilio's code says why the person was not
// reached; any other error is returned as it is.
func receiptFor(err error) (channel.Receipt, error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		code := strconv.Itoa(apiErr.Code)
		if reason, ok := refusalFor(code); ok {
			r := channel.Receipt{Reason: reason}
			if reason == channel.RateLimited {
				r.RetryAt = retryAfter(code, time.Now())
			}
			return r, nil
		}
	}
	return channel.Receipt{}, err
}

// reportReason is the reason of a delivery report that says a message failed. A code the table
// does not know still means the message did not arrive, so it counts as not reachable: the
// delivery policy then tries another route rather than losing the message.
func reportReason(code string) channel.RefusalReason {
	if reason, ok := refusalFor(code); ok {
		return reason
	}
	return channel.NotReachable
}

// templateValue makes a value fit a WhatsApp template variable: one line, no emphasis markers,
// at most maxTemplateValue characters. WhatsApp refuses a variable with a newline or a tab.
func templateValue(v string) string {
	v = strings.Map(func(r rune) rune {
		switch r {
		case '*', '_', '~', '`':
			return -1
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, v)
	v = strings.Join(strings.Fields(v), " ")
	if utf8.RuneCountInString(v) > maxTemplateValue {
		runes := []rune(v)
		v = strings.TrimSpace(string(runes[:maxTemplateValue-1])) + "…"
	}
	return v
}

// maxTemplateValue keeps a template's variable to one short line (ADR 0003 §11).
const maxTemplateValue = 60

// messenger is the part of *Client that returns Twilio's message SIDs and sends templates. The
// generated API mock does not implement it; the adapter then sends text without SIDs.
type messenger interface {
	SendMessageRefs(ctx context.Context, to, body string) ([]string, error)
	SendTemplate(ctx context.Context, to, contentSID string, values []string) (string, error)
}

var _ messenger = (*Client)(nil)

// SendMessageRefs is SendMessage returning the SID of each part sent, so a later delivery report
// can be matched to the message.
func (c *Client) SendMessageRefs(ctx context.Context, to string, text string) ([]string, error) {
	var sids []string
	for _, chunk := range SplitMessage(ToWhatsApp(text), maxTextLen) {
		sid, err := c.post(ctx, to, url.Values{"Body": {chunk}})
		if err != nil {
			return sids, err
		}
		sids = append(sids, sid)
	}
	return sids, nil
}

// SendTemplate sends the approved template with this Content SID, its variables numbered from 1.
func (c *Client) SendTemplate(ctx context.Context, to, sid string, values []string) (string, error) {
	if !contentSID.MatchString(sid) {
		return "", fmt.Errorf("%q is not a Content SID", sid)
	}
	vars := map[string]string{}
	for i, v := range values {
		vars[strconv.Itoa(i+1)] = templateValue(v)
	}
	raw, err := json.Marshal(vars)
	if err != nil {
		return "", err
	}
	return c.post(ctx, to, url.Values{"ContentSid": {sid}, "ContentVariables": {string(raw)}})
}

// post creates one message with the given fields and returns its SID.
func (c *Client) post(ctx context.Context, to string, fields url.Values) (string, error) {
	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", c.cfg.AccountSID)
	if !strings.HasPrefix(to, "whatsapp:") {
		to = "whatsapp:" + to
	}
	fields.Set("To", to)
	fields.Set("From", c.cfg.FromNumber)
	if c.cfg.StatusCallbackURL != "" {
		// BL-8: "accepted" is not "delivered"; Twilio reports the outcome to the gateway.
		fields.Set("StatusCallback", c.cfg.StatusCallbackURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(fields.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode, Message: string(body)}
		var problem struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &problem) == nil && problem.Code != 0 {
			apiErr.Code, apiErr.Message = problem.Code, problem.Message
		}
		return "", apiErr
	}
	var created struct {
		SID string `json:"sid"`
	}
	_ = json.Unmarshal(body, &created) // a missing SID only means no report can be matched
	return created.SID, nil
}
