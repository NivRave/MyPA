package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-04: the daily token caps are read from the environment; unset means no cap.
func TestLoad_UsageCaps(t *testing.T) {
	clearEnvKeepTemp(t)
	t.Chdir(t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	caps, err := cfg.UsageCaps()
	require.NoError(t, err)
	assert.Equal(t, UsageCaps{}, caps, "no cap unless one is set")

	t.Setenv("LLM_DAILY_TOKENS_PER_PERSON", "200000")
	t.Setenv("LLM_DAILY_TOKENS_PER_INSTALL", " 1000000 ")
	cfg, err = Load()
	require.NoError(t, err)
	caps, err = cfg.UsageCaps()
	require.NoError(t, err)
	assert.Equal(t, UsageCaps{PersonDaily: 200000, InstallDaily: 1000000}, caps)
}

// A cap that is not a whole number of tokens, or is negative, refuses the start for every role
// that runs turns, naming the setting; the ingress runs no turn and does not read it.
func TestValidate_UsageCaps(t *testing.T) {
	for value, want := range map[string]string{
		"abc":   "LLM_DAILY_TOKENS_PER_PERSON must be a whole number of tokens",
		"-5":    "LLM_DAILY_TOKENS_PER_PERSON must be a whole number of tokens",
		"1.5":   "LLM_DAILY_TOKENS_PER_PERSON must be a whole number of tokens",
		"200k":  "LLM_DAILY_TOKENS_PER_PERSON must be a whole number of tokens",
		"0":     "",
		"":      "",
		"50000": "",
	} {
		cfg := workerOK()
		cfg.LLM.DailyTokensPerPerson = value
		for _, role := range []string{RoleAll, RoleWorker} {
			err := cfg.Validate(role)
			if want == "" {
				assert.NoError(t, err, "%q on %s", value, role)
				continue
			}
			require.Error(t, err, "%q on %s", value, role)
			assert.Contains(t, err.Error(), want)
		}
	}
	cfg := workerOK()
	cfg.LLM.DailyTokensPerInstall = "lots"
	err := cfg.Validate(RoleWorker)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM_DAILY_TOKENS_PER_INSTALL must be a whole number of tokens")
}
