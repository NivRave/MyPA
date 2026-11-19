package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGqlgenYamlEncoding(t *testing.T) {
	// Find repo root (two levels up from internal/config)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	gqlgenPath := filepath.Join(repoRoot, "gqlgen.yml")
	data, err := os.ReadFile(gqlgenPath)
	require.NoError(t, err, "gqlgen.yml must exist and be readable")
	require.NotEmpty(t, data, "gqlgen.yml must not be empty")

	// 1. Verify byte level BOM absence
	// UTF-16LE BOM: 0xFF 0xFE
	if len(data) >= 2 {
		assert.False(t, data[0] == 0xFF && data[1] == 0xFE, "gqlgen.yml must NOT start with UTF-16LE BOM (0xFF 0xFE)")
		assert.False(t, data[0] == 0xFE && data[1] == 0xFF, "gqlgen.yml must NOT start with UTF-16BE BOM (0xFE 0xFF)")
	}
	// UTF-8 BOM: 0xEF 0xBB 0xBF
	if len(data) >= 3 {
		assert.False(t, data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF, "gqlgen.yml must NOT start with UTF-8 BOM (0xEF 0xBB 0xBF)")
	}

	// 2. Verify valid UTF-8
	require.True(t, utf8.Valid(data), "gqlgen.yml must be valid UTF-8")

	// 3. Verify YAML parsing using Viper/YAML
	v := viper.New()
	v.SetConfigType("yaml")
	err = v.ReadConfig(bytes.NewReader(data))
	require.NoError(t, err, "gqlgen.yml must parse as valid YAML without errors")

	// Verify expected keys in gqlgen.yml
	schemaConfig := v.GetStringSlice("schema")
	assert.NotEmpty(t, schemaConfig, "gqlgen.yml must specify schema paths")
	modelConfig := v.GetStringMap("model")
	assert.NotEmpty(t, modelConfig, "gqlgen.yml must specify model config")
}

func TestConfigYamlAndEnvExample(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	// Verify config.yaml
	configYamlPath := filepath.Join(repoRoot, "config", "config.yaml")
	data, err := os.ReadFile(configYamlPath)
	require.NoError(t, err, "config/config.yaml must exist")
	require.True(t, utf8.Valid(data), "config/config.yaml must be valid UTF-8")

	v := viper.New()
	v.SetConfigType("yaml")
	err = v.ReadConfig(bytes.NewReader(data))
	require.NoError(t, err, "config/config.yaml must be valid YAML")
	redirectURL := v.GetString("google.redirect_url")
	assert.Equal(t, "http://localhost:8000/auth/google/callback", redirectURL, "redirect_url must point to the app port 8000 (V4-01)")

	// Verify .env.example
	envPath := filepath.Join(repoRoot, ".env.example")
	envData, err := os.ReadFile(envPath)
	require.NoError(t, err, ".env.example must exist")
	require.True(t, utf8.Valid(envData), ".env.example must be valid UTF-8")

	envContent := string(envData)
	lines := strings.Split(envContent, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		assert.False(t, strings.HasPrefix(trimmed, "+"), "line %d in .env.example must not start with diff marker '+': %s", i+1, line)
	}

	assert.Contains(t, envContent, "ENCRYPTION_KEY=", ".env.example must contain ENCRYPTION_KEY definition")
	assert.Contains(t, envContent, "GOOGLE_REDIRECT_URL=http://localhost:8000/auth/google/callback", ".env.example must point the Google redirect at the app port (V4-01)")
	for _, gone := range []string{"GATEWAY_URL=", "ORCHESTRATOR_URL="} {
		assert.NotContains(t, envContent, gone, "V4-01 removed the proxy that read %s", gone)
	}
}
