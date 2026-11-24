package quiet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestParseWindow(t *testing.T) {
	cases := []struct {
		start, end string
		set        bool
		wantErr    bool
	}{
		{"22:00", "07:30", true, false},
		{"7:00", "9:15", true, false},
		{" 23:00 ", "06:00", true, false},
		{"", "", false, false},           // no quiet hours
		{"22:00", "", false, false},      // half a window is no window
		{"", "07:00", false, false},      // half a window is no window
		{"08:00", "08:00", false, false}, // an empty window
		{"25:00", "07:00", false, true},
		{"22:00", "7am", false, true},
		{"late", "early", false, true},
	}
	for _, c := range cases {
		w, err := ParseWindow(c.start, c.end)
		if c.wantErr {
			assert.Error(t, err, "%q-%q", c.start, c.end)
			assert.False(t, w.IsSet())
			continue
		}
		require.NoError(t, err, "%q-%q", c.start, c.end)
		assert.Equal(t, c.set, w.IsSet(), "%q-%q", c.start, c.end)
	}
}

func TestWindow_Contains(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.UTC) }

	overnight, err := ParseWindow("22:00", "07:30")
	require.NoError(t, err)
	assert.False(t, overnight.Contains(at(21, 59)))
	assert.True(t, overnight.Contains(at(22, 0)), "the start is inside")
	assert.True(t, overnight.Contains(at(23, 59)))
	assert.True(t, overnight.Contains(at(0, 0)), "it crosses midnight")
	assert.True(t, overnight.Contains(at(7, 29)))
	assert.False(t, overnight.Contains(at(7, 30)), "the end is outside")
	assert.False(t, overnight.Contains(at(12, 0)))

	afternoon, err := ParseWindow("13:00", "15:00")
	require.NoError(t, err)
	assert.False(t, afternoon.Contains(at(12, 59)))
	assert.True(t, afternoon.Contains(at(13, 0)))
	assert.True(t, afternoon.Contains(at(14, 59)))
	assert.False(t, afternoon.Contains(at(15, 0)))
	assert.False(t, afternoon.Contains(at(23, 0)))

	var none Window
	assert.False(t, none.Contains(at(3, 0)), "no quiet hours means never quiet")
}

func TestNewClock_ZoneFallsBackToInstallThenUTC(t *testing.T) {
	assert.Equal(t, "Asia/Jerusalem", NewClock(Settings{TimeZone: "Asia/Jerusalem"}, "America/New_York").Loc.String(),
		"the household's zone wins")
	assert.Equal(t, "America/New_York", NewClock(Settings{}, "America/New_York").Loc.String(),
		"an unset zone falls back to the install's")
	assert.Equal(t, "America/New_York", NewClock(Settings{TimeZone: "Mars/Olympus"}, "America/New_York").Loc.String(),
		"an invalid zone falls back to the install's")
	assert.Equal(t, "UTC", NewClock(Settings{}, "").Loc.String(), "with neither, UTC")
	assert.Equal(t, "UTC", NewClock(Settings{}, "nowhere").Loc.String())
}

func TestNewClock_InvalidQuietHoursMeanNone(t *testing.T) {
	c := NewClock(Settings{TimeZone: "UTC", Start: "late", End: "07:00"}, "")
	assert.False(t, c.Window.IsSet(), "an unreadable window holds nothing back")
	assert.False(t, c.QuietAt(time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)))
}

// The same instant is quiet in one household and not in another: the window is read on the
// person's own clock, not the install's.
func TestClock_QuietAtUsesThePersonsZone(t *testing.T) {
	jerusalem := zone(t, "Asia/Jerusalem")
	instant := time.Date(2026, 11, 20, 23, 30, 0, 0, jerusalem) // 16:30 in New York
	s := Settings{Start: "22:00", End: "07:00"}

	s.TimeZone = "Asia/Jerusalem"
	assert.True(t, NewClock(s, "UTC").QuietAt(instant))
	s.TimeZone = "America/New_York"
	assert.False(t, NewClock(s, "UTC").QuietAt(instant))
}

func TestClock_WindowEndingAt(t *testing.T) {
	jerusalem := zone(t, "Asia/Jerusalem")
	c := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, "")

	start, ok := c.WindowEndingAt(time.Date(2026, 11, 21, 7, 0, 30, 0, jerusalem))
	require.True(t, ok, "07:00 is the minute the window ends")
	assert.True(t, start.Equal(time.Date(2026, 11, 20, 22, 0, 0, 0, jerusalem)), "it started the evening before: %s", start)

	_, ok = c.WindowEndingAt(time.Date(2026, 11, 21, 7, 1, 0, 0, jerusalem))
	assert.False(t, ok)
	_, ok = c.WindowEndingAt(time.Date(2026, 11, 21, 6, 59, 0, 0, jerusalem))
	assert.False(t, ok)

	sameDay := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "13:00", End: "15:00"}, "")
	start, ok = sameDay.WindowEndingAt(time.Date(2026, 11, 21, 15, 0, 0, 0, jerusalem))
	require.True(t, ok)
	assert.True(t, start.Equal(time.Date(2026, 11, 21, 13, 0, 0, 0, jerusalem)))

	_, ok = NewClock(Settings{TimeZone: "UTC"}, "").WindowEndingAt(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC))
	assert.False(t, ok, "no window never ends")
}

func TestClock_BriefingDue(t *testing.T) {
	jerusalem := zone(t, "Asia/Jerusalem")
	day := func(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 15, 0, jerusalem) }

	plain := NewClock(Settings{TimeZone: "Asia/Jerusalem"}, "")
	assert.True(t, plain.BriefingDue(day(8, 0)), "08:00 local")
	assert.False(t, plain.BriefingDue(day(7, 59)))
	assert.False(t, plain.BriefingDue(day(8, 1)))
	assert.False(t, plain.BriefingDue(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)), "08:00 UTC is 10:00 in Jerusalem")

	lateRiser := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "23:00", End: "09:30"}, "")
	assert.False(t, lateRiser.BriefingDue(day(8, 0)), "08:00 falls in quiet hours")
	assert.True(t, lateRiser.BriefingDue(day(9, 30)), "so the briefing comes when they end")

	earlyEnd := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "08:00"}, "")
	assert.True(t, earlyEnd.BriefingDue(day(8, 0)), "a window ending at 08:00 does not cover 08:00")

	// Another zone gets its briefing at its own 08:00.
	ny := NewClock(Settings{TimeZone: "America/New_York"}, "")
	assert.True(t, ny.BriefingDue(time.Date(2026, 11, 20, 8, 0, 0, 0, zone(t, "America/New_York"))))
	assert.False(t, ny.BriefingDue(day(8, 0)))
}

// BL-49: QuietUntil is the end of the window t is in, on the person's clock, across midnight on
// either side of it.
func TestClock_QuietUntil(t *testing.T) {
	c := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "22:00", End: "07:00"}, "")
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, c.Loc) }

	end, ok := c.QuietUntil(at(20, 23, 30))
	require.True(t, ok)
	assert.True(t, end.Equal(at(21, 7, 0)), "evening part: the next morning, got %s", end)
	end, ok = c.QuietUntil(at(21, 3, 0))
	require.True(t, ok)
	assert.True(t, end.Equal(at(21, 7, 0)), "morning part: this morning, got %s", end)
	_, ok = c.QuietUntil(at(21, 7, 0))
	assert.False(t, ok, "the end is outside the window")

	day := NewClock(Settings{TimeZone: "Asia/Jerusalem", Start: "13:00", End: "15:00"}, "")
	end, ok = day.QuietUntil(time.Date(2026, 11, 20, 14, 0, 0, 0, day.Loc))
	require.True(t, ok)
	assert.True(t, end.Equal(time.Date(2026, 11, 20, 15, 0, 0, 0, day.Loc)))
	_, ok = NewClock(Settings{}, "").QuietUntil(at(20, 23, 30))
	assert.False(t, ok, "no quiet hours, nothing held")
}
