// Package quiet reads a person's clock: their time zone and their quiet hours (V4-05). The
// scheduler and the engine use it to decide when a message the assistant starts itself may go
// out. It is pure: the caller reads the settings (db.Client.QuietSettings) and passes the time.
package quiet

import (
	"fmt"
	"strings"
	"time"
)

// Briefing time: the morning briefing goes out at 08:00 on the person's clock, or when their
// quiet hours end if 08:00 falls inside them.
const (
	BriefingHour   = 8
	BriefingMinute = 0
)

// Settings are what is stored for one person: the time zone of their household (empty for the
// install default) and their quiet hours ("22:00" and "07:30", empty for none).
type Settings struct {
	TimeZone string
	Start    string
	End      string
}

// Window is a daily span of quiet hours on a wall clock, from start (inside) to end (outside). It
// may cross midnight. The zero Window is no quiet hours.
type Window struct {
	start, end int // minutes after midnight
	set        bool
}

// ParseWindow reads quiet hours in 24-hour "HH:MM". An empty start or end, or a start equal to
// the end, is no window; anything else that is not a time is an error.
func ParseWindow(start, end string) (Window, error) {
	start, end = strings.TrimSpace(start), strings.TrimSpace(end)
	if start == "" || end == "" {
		return Window{}, nil
	}
	s, err := minuteOfDay(start)
	if err != nil {
		return Window{}, err
	}
	e, err := minuteOfDay(end)
	if err != nil {
		return Window{}, err
	}
	if s == e {
		return Window{}, nil
	}
	return Window{start: s, end: e, set: true}, nil
}

func minuteOfDay(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("quiet hours: %q is not a 24-hour HH:MM time", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// IsSet reports whether there are quiet hours at all.
func (w Window) IsSet() bool { return w.set }

// Contains reports whether the wall-clock time of local falls inside the window.
func (w Window) Contains(local time.Time) bool {
	return w.containsMinute(local.Hour()*60 + local.Minute())
}

func (w Window) containsMinute(m int) bool {
	if !w.set {
		return false
	}
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end // crosses midnight
}

// String is the window as stored ("22:00-07:30"), or "" for none.
func (w Window) String() string {
	if !w.set {
		return ""
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.start/60, w.start%60, w.end/60, w.end%60)
}

// Clock is one person's clock: where they are and when they do not want to be disturbed.
type Clock struct {
	Loc    *time.Location
	Window Window
}

// NewClock builds a person's clock. The zone is the household's, else fallback (the install's
// DEFAULT_TIMEZONE), else UTC; an unset or invalid zone falls through to the next, as the
// engine's turn clock does. Quiet hours that cannot be read are treated as none: a typo must
// not hold every message back.
func NewClock(s Settings, fallback string) Clock {
	loc := time.UTC
	for _, tz := range []string{s.TimeZone, fallback} {
		if tz = strings.TrimSpace(tz); tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
				break
			}
		}
	}
	w, err := ParseWindow(s.Start, s.End)
	if err != nil {
		w = Window{}
	}
	return Clock{Loc: loc, Window: w}
}

func (c Clock) local(t time.Time) time.Time {
	if c.Loc == nil {
		return t.UTC()
	}
	return t.In(c.Loc)
}

// QuietAt reports whether t falls in the person's quiet hours, on their own clock.
func (c Clock) QuietAt(t time.Time) bool {
	return c.Window.Contains(c.local(t))
}

// WindowEndingAt reports whether t is in the minute the person's quiet hours end, and if so when
// that window started (the evening before, for a window that crosses midnight).
func (c Clock) WindowEndingAt(t time.Time) (time.Time, bool) {
	if !c.Window.set {
		return time.Time{}, false
	}
	l := c.local(t)
	if l.Hour()*60+l.Minute() != c.Window.end {
		return time.Time{}, false
	}
	day := l.Day()
	if c.Window.start > c.Window.end {
		day-- // time.Date normalises the first of the month
	}
	return time.Date(l.Year(), l.Month(), day, c.Window.start/60, c.Window.start%60, 0, 0, l.Location()), true
}

// BriefingDue reports whether t is in the minute of the person's morning briefing: 08:00 on
// their clock, or the end of their quiet hours when 08:00 falls inside them.
func (c Clock) BriefingDue(t time.Time) bool {
	due := BriefingHour*60 + BriefingMinute
	if c.Window.containsMinute(due) {
		due = c.Window.end
	}
	l := c.local(t)
	return l.Hour()*60+l.Minute() == due
}
