package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simulateProduction makes the package behave as if it were NOT running under `go test`.
func simulateProduction(t *testing.T) {
	t.Helper()
	orig := testBinaryDetector
	testBinaryDetector = func() bool { return false }
	t.Cleanup(func() { testBinaryDetector = orig })
}

func TestValidateKeyConfigured_MissingKeyInProduction_ReturnsErrorNotPanic(t *testing.T) {
	simulateProduction(t)
	t.Setenv("ENCRYPTION_KEY", "")

	var err error
	require.NotPanics(t, func() { err = ValidateKeyConfigured() })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ENCRYPTION_KEY")
}

func TestEncrypt_MissingKeyInProduction_ReturnsErrorNotPanic(t *testing.T) {
	simulateProduction(t)
	t.Setenv("ENCRYPTION_KEY", "")

	require.NotPanics(t, func() {
		_, err := Encrypt([]byte("secret"), "")
		require.Error(t, err)
		_, err = Decrypt(make([]byte, 64), "")
		require.Error(t, err)
	})
}

// ENV=test / GO_ENV=test must NOT unlock the hard-coded dev key outside a test binary.
func TestEnvVarsDoNotEnableDevFallbackKey(t *testing.T) {
	simulateProduction(t)
	t.Setenv("ENCRYPTION_KEY", "")

	for _, kv := range [][2]string{{"ENV", "test"}, {"GO_ENV", "test"}} {
		t.Run(kv[0], func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			assert.Error(t, ValidateKeyConfigured())
			_, err := Encrypt([]byte("x"), "")
			assert.Error(t, err, "%s=%s must not enable the dev fallback key", kv[0], kv[1])
		})
	}
}

func TestValidateKeyConfigured_KeySetInProduction_OK(t *testing.T) {
	simulateProduction(t)
	t.Setenv("ENCRYPTION_KEY", "a-real-production-key")
	assert.NoError(t, ValidateKeyConfigured())
}

func TestDeriveKey_MissingKeyInProduction_NoSilentFallback(t *testing.T) {
	simulateProduction(t)
	t.Setenv("ENCRYPTION_KEY", "")
	assert.Nil(t, DeriveKey(""), "DeriveKey must not silently return the dev fallback key in production")
}
