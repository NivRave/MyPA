package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmpiricalChallenge_DockerComposeNetworkIsolation(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	composePath := filepath.Join(repoRoot, "docker-compose.yml")
	data, err := os.ReadFile(composePath)
	require.NoError(t, err, "docker-compose.yml must exist")

	v := viper.New()
	v.SetConfigType("yaml")
	err = v.ReadConfig(bytes.NewReader(data))
	require.NoError(t, err, "docker-compose.yml must be valid YAML")

	services := v.GetStringMap("services")
	require.NotEmpty(t, services, "services must be declared in docker-compose.yml")

	totalPortBindingsChecked := 0

	for svcName, svcVal := range services {
		svcMap, ok := svcVal.(map[string]interface{})
		require.True(t, ok, "service %s must be a map", svcName)

		rawPorts, hasPorts := svcMap["ports"]
		if !hasPorts || rawPorts == nil {
			continue
		}

		portsSlice, ok := rawPorts.([]interface{})
		require.True(t, ok, "ports for service %s must be a list", svcName)

		for _, p := range portsSlice {
			portStr, ok := p.(string)
			require.True(t, ok, "port binding must be a string")
			totalPortBindingsChecked++

			t.Logf("Checking port binding for service %s: %s", svcName, portStr)

			// Strict check: every exposed host port MUST start with 127.0.0.1:
			assert.True(t, strings.HasPrefix(portStr, "127.0.0.1:"),
				"Port binding '%s' in service '%s' MUST be strictly prefixed with '127.0.0.1:'", portStr, svcName)

			assert.False(t, strings.HasPrefix(portStr, "0.0.0.0:"),
				"Port binding '%s' in service '%s' must NOT expose to 0.0.0.0", portStr, svcName)
		}
	}

	assert.GreaterOrEqual(t, totalPortBindingsChecked, 8, "Expected at least 8 published service ports to be checked")
	t.Logf("Total verified 127.0.0.1 port bindings: %d", totalPortBindingsChecked)
}

func TestEmpiricalChallenge_DockerComposeEnvInterpolation(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	composePath := filepath.Join(repoRoot, "docker-compose.yml")
	data, err := os.ReadFile(composePath)
	require.NoError(t, err)

	content := string(data)

	// 1. Check Postgres credential interpolation
	assert.Contains(t, content, "${POSTGRES_USER:-mypa}", "Postgres user must interpolate POSTGRES_USER")
	assert.Contains(t, content, "${POSTGRES_PASSWORD:-mypa_password}", "Postgres password must interpolate POSTGRES_PASSWORD")
	assert.Contains(t, content, "${POSTGRES_DB:-mypa}", "Postgres DB must interpolate POSTGRES_DB")

	// 2. Check RabbitMQ credential interpolation
	assert.Contains(t, content, "${RABBITMQ_DEFAULT_USER:-mypa}", "RabbitMQ user must interpolate RABBITMQ_DEFAULT_USER")
	assert.Contains(t, content, "${RABBITMQ_DEFAULT_PASS:-mypa_dev}", "RabbitMQ password must interpolate RABBITMQ_DEFAULT_PASS")

	// 3. Check DATABASE_URL and RABBITMQ_URL interpolation across dependent services
	assert.Contains(t, content, "DATABASE_URL=${DATABASE_URL:-postgres://", "DATABASE_URL must have fallback interpolation")
	assert.Contains(t, content, "RABBITMQ_URL=${RABBITMQ_URL:-amqp://", "RABBITMQ_URL must have fallback interpolation")

	// 4. Check audit-worker named volume
	assert.Contains(t, content, "audit_archives:/app/archives", "audit-worker must mount named volume audit_archives")
	assert.Contains(t, content, "audit_archives:", "top-level volumes must declare audit_archives")
}
