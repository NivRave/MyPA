package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractCronSchedule(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:     "plain 5-field cron",
			input:    "0 9 * * 1",
			expected: "0 9 * * 1",
		},
		{
			name:     "json with cron field",
			input:    `{"cron": "*/15 * * * *"}`,
			expected: "*/15 * * * *",
		},
		{
			name:     "json with expression field",
			input:    `{"expression": "0 0 * * *"}`,
			expected: "0 0 * * *",
		},
		{
			name:     "json with schedule field",
			input:    `{"schedule": "30 4 1 * *"}`,
			expected: "30 4 1 * *",
		},
		{
			name:     "descriptor cron",
			input:    "@daily",
			expected: "@daily",
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
		{
			name:        "invalid cron expression",
			input:       "not a cron",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ExtractCronSchedule(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, res)
			}
		})
	}
}
