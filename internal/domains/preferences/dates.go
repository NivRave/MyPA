package preferences

import (
	"strings"
	"time"
)

func calculateDaysUntil(dateStr string, now time.Time) (int, bool) {
	dStr := strings.TrimSpace(dateStr)
	var month time.Month
	var day int

	// Format: YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", dStr); err == nil {
		month, day = t.Month(), t.Day()
	} else if t, err := time.Parse("01-02", dStr); err == nil {
		// Format: MM-DD
		month, day = t.Month(), t.Day()
	} else if t, err := time.Parse("2006/01/02", dStr); err == nil {
		month, day = t.Month(), t.Day()
	} else {
		return 0, false
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	targetThisYear := time.Date(now.Year(), month, day, 0, 0, 0, 0, now.Location())

	if targetThisYear.Before(today) {
		targetNextYear := time.Date(now.Year()+1, month, day, 0, 0, 0, 0, now.Location())
		days := int(targetNextYear.Sub(today).Hours() / 24)
		return days, true
	}

	days := int(targetThisYear.Sub(today).Hours() / 24)
	return days, true
}
