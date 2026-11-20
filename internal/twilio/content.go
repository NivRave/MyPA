package twilio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// contentAPIBase is Twilio's Content API, where WhatsApp templates live (V4-41).
const contentAPIBase = "https://content.twilio.com/v1/Content"

// ContentAPI is the part of Twilio's Content API MyPA uses: reading a template's WhatsApp approval
// (/doctor) and creating and submitting MyPA's templates (the guided submission). *Client
// implements it.
type ContentAPI interface {
	TemplateApproval(ctx context.Context, sid string) (Approval, error)
	CreateTemplate(ctx context.Context, t Template) (sid string, err error)
	SubmitTemplate(ctx context.Context, sid string, t Template) error
}

var _ ContentAPI = (*Client)(nil)

// TemplateApproval asks Twilio for the WhatsApp approval of the template with this Content SID.
func (c *Client) TemplateApproval(ctx context.Context, sid string) (Approval, error) {
	if !contentSID.MatchString(sid) {
		return Approval{}, fmt.Errorf("%q is not a Content SID", sid)
	}
	var body struct {
		WhatsApp struct {
			Status          string `json:"status"`
			RejectionReason string `json:"rejection_reason"`
		} `json:"whatsapp"`
	}
	if err := c.content(ctx, http.MethodGet, contentAPIBase+"/"+url.PathEscape(sid)+"/ApprovalRequests", nil, &body); err != nil {
		return Approval{}, err
	}
	return Approval{Status: body.WhatsApp.Status, Reason: body.WhatsApp.RejectionReason}, nil
}

// CreateTemplate creates one of MyPA's templates in the owner's account and returns its SID.
func (c *Client) CreateTemplate(ctx context.Context, t Template) (string, error) {
	variables := map[string]string{}
	for i, s := range t.Samples {
		variables[strconv.Itoa(i+1)] = s
	}
	req := map[string]any{
		"friendly_name": t.Name,
		"language":      t.Language,
		"variables":     variables,
		"types":         map[string]any{"twilio/text": map[string]string{"body": t.Body}},
	}
	var body struct {
		SID string `json:"sid"`
	}
	if err := c.content(ctx, http.MethodPost, contentAPIBase, req, &body); err != nil {
		return "", err
	}
	if !contentSID.MatchString(body.SID) {
		return "", fmt.Errorf("twilio answered without a Content SID")
	}
	return body.SID, nil
}

// SubmitTemplate asks WhatsApp, through Twilio, to approve a created template.
func (c *Client) SubmitTemplate(ctx context.Context, sid string, t Template) error {
	if !contentSID.MatchString(sid) {
		return fmt.Errorf("%q is not a Content SID", sid)
	}
	req := map[string]string{"name": t.Name, "category": t.Category}
	return c.content(ctx, http.MethodPost, contentAPIBase+"/"+url.PathEscape(sid)+"/ApprovalRequests/whatsapp", req, nil)
}

// content makes one Content API call with the account's credentials. The API takes and returns
// JSON; an answer outside 2xx is an error carrying Twilio's own message.
func (c *Client) content(ctx context.Context, method, endpoint string, in, out any) error {
	var reqBody io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(c.cfg.AccountSID, c.cfg.AuthToken)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("asking Twilio: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var problem struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &problem) == nil && problem.Message != "" {
			return fmt.Errorf("twilio answered %d: %s", resp.StatusCode, problem.Message)
		}
		return fmt.Errorf("twilio answered %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("reading Twilio's answer: %w", err)
		}
	}
	return nil
}
