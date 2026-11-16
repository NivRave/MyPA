package db

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// BL-2: GORM's default logger printed coloured plain text with the bound values (phone numbers)
// into the JSON log stream, and logged every normal "record not found" as an error.
func TestOpen_SQLLogsAreJSONWithoutValuesOrNotFoundNoise(t *testing.T) {
	dsn := newEmptyTestDatabase(t)

	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c, err := Open(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.DB.Exec("CREATE TABLE people (id serial PRIMARY KEY, phone text)").Error)

	const phone = "+15550100123" // NANP 555-01xx: reserved for fiction

	// 1. A normal miss: not an incident, must not be logged.
	var row struct {
		ID    int
		Phone string
	}
	err = c.DB.Table("people").Where("phone = ?", phone).First(&row).Error
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	// 2. A real SQL error with a bound value: logged (as JSON), but without the value.
	err = c.DB.Exec("SELECT * FROM missing_table WHERE phone = ?", phone).Error
	require.Error(t, err)

	out := buf.String()
	assert.Contains(t, out, "missing_table", "real SQL errors are still logged")
	assert.NotContains(t, out, phone, "bound values must not reach the logs")
	assert.NotContains(t, out, "record not found", "ErrRecordNotFound is normal control flow")
	assert.NotContains(t, out, "\x1b[", "no ANSI colour codes")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		assert.True(t, json.Valid([]byte(line)), "every log line is JSON: %q", line)
	}
}
