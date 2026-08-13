package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/models"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
	"golang.org/x/oauth2"
)

// Client wraps the Google Calendar API client.
type Client struct {
	svc *calendar.Service
}

// NewClient initializes a new Calendar client using an authenticated token source.
func NewClient(ctx context.Context, ts oauth2.TokenSource) (*Client, error) {
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("failed to create calendar service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// CreateEvent inserts a new event into the primary calendar.
func (c *Client) CreateEvent(ctx context.Context, ev models.CalendarEvent) (string, error) {
	// Build the Google Calendar event structure
	event := &calendar.Event{
		Summary:     ev.Title,
		Description: ev.Description,
		Start: &calendar.EventDateTime{
			DateTime: ev.StartTime,
			TimeZone: ev.Timezone,
		},
		End: &calendar.EventDateTime{
			DateTime: ev.EndTime,
			TimeZone: ev.Timezone,
		},
	}

	// Insert the event into the user's "primary" calendar
	res, err := c.svc.Events.Insert("primary", event).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("calendar api error: %w", err)
	}

	return res.HtmlLink, nil
}

// Helper functions to convert local time formats into RFC3339 if needed
func FormatDateTime(t time.Time) string {
	return t.Format(time.RFC3339)
}
