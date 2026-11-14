package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			parsed := net.ParseIP(tc.ip)
			require.NotNil(t, parsed)
			assert.Equal(t, tc.blocked, IsBlockedIP(parsed))
		})
	}
}

func TestIsBlockedHostname(t *testing.T) {
	SetAllowLoopbackForTesting(false)
	defer SetAllowLoopbackForTesting(true)

	tests := []struct {
		host    string
		blocked bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"host.docker.internal", true},
		{"postgres", true},     // single-label container
		{"redis", true},        // single-label container
		{"orchestrator", true}, // single-label container
		{"service.local", true},
		{"app.internal", true},
		{"api.github.com", false},
		{"example.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			assert.Equal(t, tc.blocked, IsBlockedHostname(tc.host))
		})
	}
}

func TestValidateURL(t *testing.T) {
	SetAllowLoopbackForTesting(false)
	defer SetAllowLoopbackForTesting(true)

	blockedURLs := []string{
		"http://127.0.0.1:8080/secret",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5:5432/",
		"http://192.168.1.1/admin",
		"http://postgres:5432/db",
		"http://redis:6379/",
		"http://internal.service.local/",
		"ftp://example.com/file",
		"file:///etc/passwd",
	}

	for _, u := range blockedURLs {
		t.Run("Blocked_"+u, func(t *testing.T) {
			err := ValidateURL(u)
			assert.Error(t, err, "URL should be blocked: %s", u)
		})
	}

	allowedURLs := []string{
		"https://api.github.com/repos",
		"http://example.com/",
	}

	for _, u := range allowedURLs {
		t.Run("Allowed_"+u, func(t *testing.T) {
			err := ValidateURL(u)
			assert.NoError(t, err, "URL should be allowed: %s", u)
		})
	}
}

func TestSafeClient_BlockedAndAllowed(t *testing.T) {
	// 1. With loopback disabled, local request must fail
	SetAllowLoopbackForTesting(false)
	client := SafeClient(5 * time.Second)

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:9999", nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF blocked")

	// 2. With loopback enabled for testing, local test server request succeeds
	SetAllowLoopbackForTesting(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	testReq, err := http.NewRequestWithContext(context.Background(), "GET", ts.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(testReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
