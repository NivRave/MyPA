package toolutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmpirical_TimezoneNormalization_AsiaJerusalem tests parsing naive dates/times
// in Asia/Jerusalem during both Standard Time (UTC+2) and Daylight Saving Time (UTC+3).
func TestEmpirical_TimezoneNormalization_AsiaJerusalem(t *testing.T) {
	locJerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)

	// Case 1: Winter / Standard Time (November 8, 2026) -> IST (UTC+2)
	// Naive local input: 09:00:00
	// Expected UTC: 07:00:00 (prevents 2-hour reminder delay)
	winterInput := "2026-11-08 09:00:00"
	winterTime, err := ParseTimeInLocation(winterInput, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 2026, winterTime.Year())
	assert.Equal(t, time.November, winterTime.Month())
	assert.Equal(t, 8, winterTime.Day())
	assert.Equal(t, 7, winterTime.Hour(), "09:00 IST in winter must normalize to 07:00 UTC")
	assert.Equal(t, 0, winterTime.Minute())
	assert.Equal(t, time.UTC, winterTime.Location())

	// Case 2: Summer / Daylight Saving Time (July 15, 2026) -> IDT (UTC+3)
	// Naive local input: 09:00:00
	// Expected UTC: 06:00:00 (prevents 3-hour reminder delay)
	summerInput := "2026-07-15 09:00:00"
	summerTime, err := ParseTimeInLocation(summerInput, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 2026, summerTime.Year())
	assert.Equal(t, time.July, summerTime.Month())
	assert.Equal(t, 15, summerTime.Day())
	assert.Equal(t, 6, summerTime.Hour(), "09:00 IDT in summer must normalize to 06:00 UTC")
	assert.Equal(t, 0, summerTime.Minute())
	assert.Equal(t, time.UTC, summerTime.Location())

	// Case 3: ISO 'T' separator without zone offset (e.g. "2026-11-08T09:00:00")
	isoInput := "2026-11-08T09:00:00"
	isoTime, err := ParseTimeInLocation(isoInput, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 7, isoTime.Hour(), "ISO naive 09:00 IST must normalize to 07:00 UTC")

	// Case 4: Date and minute only (e.g. "2026-11-08 14:30")
	minInput := "2026-11-08 14:30"
	minTime, err := ParseTimeInLocation(minInput, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 12, minTime.Hour())
	assert.Equal(t, 30, minTime.Minute())

	// Case 5: Date only (e.g. "2026-11-08") -> Midnight in Jerusalem = 22:00 previous day UTC
	dateOnlyInput := "2026-11-08"
	dateOnlyTime, err := ParseTimeInLocation(dateOnlyInput, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 7, dateOnlyTime.Day())
	assert.Equal(t, 22, dateOnlyTime.Hour(), "Midnight in Jerusalem (UTC+2) is 22:00 previous day in UTC")
}

// TestEmpirical_TimezoneNormalization_AmericaNewYork tests parsing naive dates/times
// in America/New_York during both Standard Time (EST, UTC-5) and Daylight Saving Time (EDT, UTC-4).
func TestEmpirical_TimezoneNormalization_AmericaNewYork(t *testing.T) {
	locNY, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Case 1: Winter / Standard Time (November 8, 2026) -> EST (UTC-5)
	// Naive local input: 09:00:00
	// Expected UTC: 14:00:00 (09:00 + 5 hours)
	winterInput := "2026-11-08 09:00:00"
	winterTime, err := ParseTimeInLocation(winterInput, locNY)
	require.NoError(t, err)
	assert.Equal(t, 14, winterTime.Hour(), "09:00 EST must normalize to 14:00 UTC")
	assert.Equal(t, time.UTC, winterTime.Location())

	// Case 2: Summer / Daylight Saving Time (July 15, 2026) -> EDT (UTC-4)
	// Naive local input: 09:00:00
	// Expected UTC: 13:00:00 (09:00 + 4 hours)
	summerInput := "2026-07-15 09:00:00"
	summerTime, err := ParseTimeInLocation(summerInput, locNY)
	require.NoError(t, err)
	assert.Equal(t, 13, summerTime.Hour(), "09:00 EDT must normalize to 13:00 UTC")
	assert.Equal(t, time.UTC, summerTime.Location())

	// Case 3: Date only in New York -> Midnight in NY is 05:00 UTC same day
	dateOnlyInput := "2026-11-08"
	dateOnlyTime, err := ParseTimeInLocation(dateOnlyInput, locNY)
	require.NoError(t, err)
	assert.Equal(t, 8, dateOnlyTime.Day())
	assert.Equal(t, 5, dateOnlyTime.Hour(), "Midnight in NY (UTC-5) is 05:00 UTC same day")
}

// TestEmpirical_TimezoneNormalization_ExplicitOffsetPrecedence verifies that when
// an explicit offset is provided in the string, it overrides the location parameter.
func TestEmpirical_TimezoneNormalization_ExplicitOffsetPrecedence(t *testing.T) {
	locNY, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Input explicitly specifies +02:00, even though loc is America/New_York
	explicitPlus := "2026-11-08T09:00:00+02:00"
	t1, err := ParseTimeInLocation(explicitPlus, locNY)
	require.NoError(t, err)
	assert.Equal(t, 7, t1.Hour(), "Explicit +02:00 must yield 07:00 UTC regardless of locNY")

	// Input explicitly specifies -05:00, even though loc is Asia/Jerusalem
	locJerusalem, err := time.LoadLocation("Asia/Jerusalem")
	require.NoError(t, err)

	explicitMinus := "2026-11-08T09:00:00-05:00"
	t2, err := ParseTimeInLocation(explicitMinus, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 14, t2.Hour(), "Explicit -05:00 must yield 14:00 UTC regardless of locJerusalem")

	// Input explicitly specifies Z
	explicitZ := "2026-11-08T09:00:00Z"
	t3, err := ParseTimeInLocation(explicitZ, locJerusalem)
	require.NoError(t, err)
	assert.Equal(t, 9, t3.Hour(), "Explicit Z must yield 09:00 UTC")
}

// TestEmpirical_TimezoneNormalization_EdgeCases verifies nil location and error handling.
func TestEmpirical_TimezoneNormalization_EdgeCases(t *testing.T) {
	// Nil location defaults to UTC
	t1, err := ParseTimeInLocation("2026-11-08 09:00:00", nil)
	require.NoError(t, err)
	assert.Equal(t, 9, t1.Hour())
	assert.Equal(t, time.UTC, t1.Location())

	// Empty string returns error
	_, err = ParseTimeInLocation("", nil)
	require.Error(t, err)

	// Whitespace only returns error
	_, err = ParseTimeInLocation("   ", nil)
	require.Error(t, err)

	// Invalid format returns error
	_, err = ParseTimeInLocation("not-a-valid-date", nil)
	require.Error(t, err)
}
