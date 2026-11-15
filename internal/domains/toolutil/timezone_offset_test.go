package toolutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimezoneOffsetNormalization(t *testing.T) {
	t.Run("Asia_Jerusalem_StandardTime_Winter", func(t *testing.T) {
		loc, err := time.LoadLocation("Asia/Jerusalem")
		if err != nil {
			t.Logf("time.LoadLocation(Asia/Jerusalem) returned error on this host: %v (falling back to FixedZone UTC+2)", err)
			loc = time.FixedZone("IST", 2*3600)
		}

		// 2026-11-08 is in November (Standard Time in Israel: UTC+2)
		// 09:00:00 local time in Jerusalem must normalize to 07:00:00 UTC
		got, err := ParseTimeInLocation("2026-11-08 09:00:00", loc)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, got.Location())
		assert.Equal(t, 2026, got.Year())
		assert.Equal(t, time.November, got.Month())
		assert.Equal(t, 8, got.Day())
		assert.Equal(t, 7, got.Hour(), "09:00:00 Jerusalem (UTC+2) must normalize to 07:00:00 UTC")
		assert.Equal(t, 0, got.Minute())
		assert.Equal(t, 0, got.Second())
	})

	t.Run("America_New_York_StandardTime_Winter", func(t *testing.T) {
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Logf("time.LoadLocation(America/New_York) returned error on this host: %v (falling back to FixedZone UTC-5)", err)
			loc = time.FixedZone("EST", -5*3600)
		}

		// 2026-11-08 is in November (EST: UTC-5)
		// 09:00:00 local time in New York must normalize to 14:00:00 UTC
		got, err := ParseTimeInLocation("2026-11-08 09:00:00", loc)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, got.Location())
		assert.Equal(t, 14, got.Hour(), "09:00:00 New York (UTC-5) must normalize to 14:00:00 UTC")
	})

	t.Run("ExplicitRFC3339OverridesLocation", func(t *testing.T) {
		loc := time.FixedZone("IST", 2*3600)
		// RFC3339 with explicit -04:00 offset
		got, err := ParseTimeInLocation("2026-11-08T09:00:00-04:00", loc)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, got.Location())
		assert.Equal(t, 13, got.Hour(), "09:00 -04:00 must convert to 13:00 UTC regardless of loc parameter")
	})

	t.Run("ISODateOnlyInterpretedInLocation", func(t *testing.T) {
		loc := time.FixedZone("IST", 2*3600)
		got, err := ParseTimeInLocation("2026-11-08", loc)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, got.Location())
		// Midnight in UTC+2 is 22:00 of the previous day in UTC
		assert.Equal(t, 22, got.Hour())
		assert.Equal(t, 7, got.Day())
	})
}
