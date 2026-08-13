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

// ListEvents retrieves events from the primary calendar within the specified time range.
// Optional query string 'q' can be used to filter events.
func (c *Client) ListEvents(ctx context.Context, timeMin, timeMax string, q string) ([]*calendar.Event, error) {
	req := c.svc.Events.List("primary").
		ShowDeleted(false).
		SingleEvents(true).
		OrderBy("startTime")

	if timeMin != "" {
		req = req.TimeMin(timeMin)
	}
	if timeMax != "" {
		req = req.TimeMax(timeMax)
	}
	if q != "" {
		req = req.Q(q)
	}

	events, err := req.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list calendar events: %w", err)
	}

	return events.Items, nil
}

// UpdateEvent updates an existing event in the primary calendar using Patch for partial updates.
func (c *Client) UpdateEvent(ctx context.Context, eventID string, ev models.CalendarEvent) error {
	event := &calendar.Event{}
	
	if ev.Title != "" {
		event.Summary = ev.Title
	}
	if ev.Description != "" {
		event.Description = ev.Description
	}
	if ev.StartTime != "" {
		event.Start = &calendar.EventDateTime{
			DateTime: ev.StartTime,
			TimeZone: ev.Timezone,
		}
	}
	if ev.EndTime != "" {
		event.End = &calendar.EventDateTime{
			DateTime: ev.EndTime,
			TimeZone: ev.Timezone,
		}
	}

	_, err := c.svc.Events.Patch("primary", eventID, event).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to update calendar event: %w", err)
	}

	return nil
}

// DeleteEvent removes an event from the primary calendar.
func (c *Client) DeleteEvent(ctx context.Context, eventID string) error {
	err := c.svc.Events.Delete("primary", eventID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete calendar event: %w", err)
	}

	return nil
}
