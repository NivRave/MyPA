package events

import (
	"encoding/json"
	"strings"
)

// rowImage is the row the event is about: its after-image, or its before-image when it has no
// after-image (a delete).
func (e MutationEvent) rowImage() string {
	if strings.TrimSpace(e.AfterJSON) != "" {
		return e.AfterJSON
	}
	return e.BeforeJSON
}

// Unshared reports whether the event is about a row its person did not share
// (`"is_shared": false`): an unshared transaction or calendar event, created, changed or deleted
// (BL-44, BL-67). A row without the field, or an image that is not a JSON object, is not.
func (e MutationEvent) Unshared() bool {
	img := strings.TrimSpace(e.rowImage())
	if img == "" {
		return false
	}
	var row struct {
		IsShared *bool `json:"is_shared"`
	}
	if err := json.Unmarshal([]byte(img), &row); err != nil {
		return false
	}
	return row.IsShared != nil && !*row.IsShared
}

// Urgent reports whether the event is about a row marked urgent (`"urgent": true`, as a reminder
// is, V4-05): a message about it reaches its recipient even during their quiet hours (BL-49).
func (e MutationEvent) Urgent() bool {
	img := strings.TrimSpace(e.rowImage())
	if img == "" {
		return false
	}
	var row struct {
		Urgent bool `json:"urgent"`
	}
	if err := json.Unmarshal([]byte(img), &row); err != nil {
		return false
	}
	return row.Urgent
}
