package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	blockedCIDRs = []string{
		"127.0.0.0/8",    // IPv4 Loopback
		"10.0.0.0/8",     // RFC1918 Private Class A
		"172.16.0.0/12",  // RFC1918 Private Class B
		"192.168.0.0/16", // RFC1918 Private Class C
		"169.254.0.0/16", // IPv4 Link-Local & Cloud Metadata (169.254.169.254)
		"0.0.0.0/8",      // Current network
		"100.64.0.0/10",  // Carrier-Grade NAT
		"198.18.0.0/15",  // Benchmark testing
		"::1/128",        // IPv6 Loopback
		"fc00::/7",       // IPv6 Unique Local
		"fe80::/10",      // IPv6 Link-Local
	}

	blockedIPNets []*net.IPNet

	blockedHostnames = []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"host.docker.internal",
		"docker.for.mac.localhost",
		"docker.for.win.localhost",
		"gateway.docker.internal",
	}

	blockedSuffixes = []string{
		".localhost",
		".local",
		".internal",
	}

	mu                      sync.RWMutex
	allowLoopbackForTesting bool
)

func init() {
	for _, cidr := range blockedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			blockedIPNets = append(blockedIPNets, ipNet)
		}
	}
}

// SetAllowLoopbackForTesting configures whether loopback requests are permitted for unit tests.
func SetAllowLoopbackForTesting(allow bool) {
	mu.Lock()
	defer mu.Unlock()
	allowLoopbackForTesting = allow
}

func isLoopbackAllowed() bool {
	mu.RLock()
	defer mu.RUnlock()
	return allowLoopbackForTesting
}

// IsBlockedIP verifies whether the provided IP address is in a reserved, private, or prohibited range.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
			return true
		}
		for _, bNet := range blockedIPNets {
			if bNet.Contains(ip4) {
				return true
			}
		}
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	for _, bNet := range blockedIPNets {
		if bNet.Contains(ip) {
			return true
		}
	}
	return false
}

// IsBlockedHostname tests whether a hostname is prohibited (loopback, single-label Docker containers, or internal suffixes).
func IsBlockedHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(h, ":") {
		if sh, _, err := net.SplitHostPort(h); err == nil {
			h = sh
		}
	}

	if isLoopbackAllowed() && (h == "localhost" || h == "127.0.0.1" || h == "::1") {
		return false
	}

	// Prohibit single-label container hostnames (e.g. "postgres", "redis", "orchestrator", "gateway")
	if !strings.Contains(h, ".") && h != "localhost" {
		return true
	}

	for _, b := range blockedHostnames {
		if h == b {
			return true
		}
	}
	for _, s := range blockedSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// ValidateURL performs static syntactic and DNS validation against a target URL string.
func ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if strings.TrimSpace(hostname) == "" {
		return errors.New("URL missing hostname")
	}

	if isLoopbackAllowed() && (hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1") {
		return nil
	}

	if IsBlockedHostname(hostname) {
		return fmt.Errorf("SSRF blocked: hostname %q is prohibited", hostname)
	}

	if ip := net.ParseIP(hostname); ip != nil {
		if IsBlockedIP(ip) {
			return fmt.Errorf("SSRF blocked: IP %s is private or reserved", ip.String())
		}
		return nil
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("SSRF DNS lookup failed for %q: %w", hostname, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("SSRF blocked: no IP addresses resolved for %q", hostname)
	}
	for _, ip := range ips {
		if IsBlockedIP(ip) {
			return fmt.Errorf("SSRF blocked: host %q resolves to prohibited IP %s", hostname, ip.String())
		}
	}
	return nil
}

// SafeClient constructs a production-grade http.Client with connection-level DNS resolution
// and socket dialing safeguards preventing SSRF.
func SafeClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}

			if isLoopbackAllowed() && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
				return dialer.DialContext(ctx, network, addr)
			}

			if IsBlockedHostname(host) {
				return nil, fmt.Errorf("SSRF blocked: host %q is not permitted", host)
			}

			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("SSRF DNS resolution failed for %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("SSRF blocked: no IP addresses resolved for %q", host)
			}

			for _, ip := range ips {
				if IsBlockedIP(ip) {
					return nil, fmt.Errorf("SSRF blocked: host %q resolved to prohibited IP %s", host, ip.String())
				}
			}

			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		ResponseHeaderTimeout: timeout,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if !isLoopbackAllowed() {
				if err := ValidateURL(req.URL.String()); err != nil {
					return fmt.Errorf("redirect blocked by SSRF guard: %w", err)
				}
			}
			return nil
		},
	}
}
