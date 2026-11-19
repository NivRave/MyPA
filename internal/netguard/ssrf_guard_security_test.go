package netguard

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdversarial_SEC05_IPRanges(t *testing.T) {
	testCases := []struct {
		name    string
		ip      string
		blocked bool
	}{
		// IPv4 Loopback
		{"IPv4_Loopback_1", "127.0.0.1", true},
		{"IPv4_Loopback_2", "127.0.0.2", true},
		{"IPv4_Loopback_High", "127.255.255.254", true},
		{"IPv4_Unspecified", "0.0.0.0", true},

		// IPv6 Loopback & Local
		{"IPv6_Loopback", "::1", true},
		{"IPv6_LinkLocal", "fe80::1", true},
		{"IPv6_UniqueLocal", "fc00::1", true},
		{"IPv6_UniqueLocal_fd", "fd12:3456:789a::1", true},

		// RFC1918 Class A (10.0.0.0/8)
		{"RFC1918_A_Min", "10.0.0.1", true},
		{"RFC1918_A_Mid", "10.128.0.50", true},
		{"RFC1918_A_Max", "10.255.255.254", true},

		// RFC1918 Class B (172.16.0.0/12)
		{"RFC1918_B_Min", "172.16.0.1", true},
		{"RFC1918_B_Mid", "172.24.10.1", true},
		{"RFC1918_B_Max", "172.31.255.254", true},

		// RFC1918 Class C (192.168.0.0/16)
		{"RFC1918_C_Min", "192.168.0.1", true},
		{"RFC1918_C_Mid", "192.168.1.1", true},
		{"RFC1918_C_Max", "192.168.255.254", true},

		// Cloud Metadata (169.254.169.254) & Link-Local (169.254.0.0/16)
		{"CloudMetadata_AWS_GCP_Azure", "169.254.169.254", true},
		{"IPv4_LinkLocal_Min", "169.254.0.1", true},
		{"IPv4_LinkLocal_Max", "169.254.255.254", true},

		// Carrier-Grade NAT (100.64.0.0/10)
		{"CGNAT_Min", "100.64.0.1", true},
		{"CGNAT_Mid", "100.100.50.25", true},
		{"CGNAT_Max", "100.127.255.254", true},

		// Benchmark / Prohibited (198.18.0.0/15)
		{"Benchmark_Test", "198.18.0.1", true},

		// Legitimate Public IPs (Allowed)
		{"Cloudflare_DNS", "1.1.1.1", false},
		{"Google_DNS", "8.8.8.8", false},
		{"Quad9_DNS", "9.9.9.9", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := net.ParseIP(tc.ip)
			require.NotNil(t, parsed, "Failed to parse IP: %s", tc.ip)
			assert.Equal(t, tc.blocked, IsBlockedIP(parsed), "IP block mismatch for: %s", tc.ip)
		})
	}
}

func TestAdversarial_SEC05_Hostnames(t *testing.T) {
	SetAllowLoopbackForTesting(false)
	defer SetAllowLoopbackForTesting(true)

	testCases := []struct {
		hostname string
		blocked  bool
	}{
		// Single-label Docker container hostnames
		{"postgres", true},
		{"postgres:5432", true},
		{"redis", true},
		{"redis:6379", true},
		{"rabbitmq", true},
		{"rabbitmq:5672", true},
		{"orchestrator", true},
		{"gateway", true},
		{"proxy", true},
		{"db", true},
		{"app", true},

		// Docker internal hostnames
		{"host.docker.internal", true},
		{"gateway.docker.internal", true},
		{"docker.for.mac.localhost", true},
		{"docker.for.win.localhost", true},

		// Prohibited domain suffixes
		{"myservice.internal", true},
		{"database.local", true},
		{"auth.localhost", true},

		// Public multi-label domains (Allowed)
		{"google.com", false},
		{"api.github.com", false},
		{"subdomain.example.org", false},
	}

	for _, tc := range testCases {
		t.Run("Hostname_"+tc.hostname, func(t *testing.T) {
			assert.Equal(t, tc.blocked, IsBlockedHostname(tc.hostname), "Hostname block mismatch for: %s", tc.hostname)
		})
	}
}

func TestAdversarial_SEC05_ValidateURL(t *testing.T) {
	SetAllowLoopbackForTesting(false)
	defer SetAllowLoopbackForTesting(true)

	adversarialURLs := []struct {
		name string
		url  string
	}{
		// Loopback
		{"Loopback_IPv4", "http://127.0.0.1:8080/admin"},
		{"Loopback_Localhost", "http://localhost:3000/"},
		{"Loopback_IPv6", "http://[::1]:8080/api"},

		// RFC1918
		{"RFC1918_A", "http://10.0.0.1/sensitive"},
		{"RFC1918_B", "http://172.16.0.1/backup"},
		{"RFC1918_C", "http://192.168.1.1/config"},

		// Cloud Metadata
		{"AWS_GCP_Azure_Metadata", "http://169.254.169.254/latest/meta-data/"},
		{"GCP_Metadata_Flavor", "http://169.254.169.254/computeMetadata/v1/"},

		// Carrier-Grade NAT
		{"CGNAT_Gateway", "http://100.64.0.1:8080/status"},

		// Docker Container Hostnames
		{"Container_Postgres", "http://postgres:5432/db"},
		{"Container_Redis", "http://redis:6379/keys"},
		{"Container_RabbitMQ", "http://rabbitmq:5672/api"},
		{"Container_App", "http://app:8000/metrics"},

		// Dangerous Schemes
		{"Scheme_File", "file:///etc/passwd"},
		{"Scheme_Gopher", "gopher://127.0.0.1:70/"},
		{"Scheme_FTP", "ftp://ftp.example.com/file"},
		{"Scheme_SSH", "ssh://user@example.com"},

		// Missing Hostname
		{"Missing_Hostname", "http:///path"},
	}

	for _, tc := range adversarialURLs {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url)
			assert.Error(t, err, "ValidateURL should have rejected: %s", tc.url)
		})
	}
}

func TestAdversarial_SEC05_SafeClient_Enforcement(t *testing.T) {
	SetAllowLoopbackForTesting(false)
	defer SetAllowLoopbackForTesting(true)

	client := SafeClient(1 * time.Second)

	forbiddenTargets := []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1:80",
		"http://172.16.0.1:80",
		"http://192.168.1.1:80",
		"http://100.64.0.1:80",
		"http://postgres:5432",
		"http://redis:6379",
		"http://rabbitmq:5672",
	}

	for _, target := range forbiddenTargets {
		t.Run("SafeClientBlocked_"+target, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), "GET", target, nil)
			require.NoError(t, err)

			_, err = client.Do(req)
			assert.Error(t, err, "SafeClient must block request to %s", target)
			assert.Contains(t, err.Error(), "SSRF blocked", "Error must cite SSRF guard for %s", target)
		})
	}
}
