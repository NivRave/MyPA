package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// V4-40: one compose file for a home PC and a server, chosen by settings. These tests read
// docker-compose.yml, deploy/ and .env.example as committed.

type composeIngressService struct {
	Image       string         `yaml:"image"`
	Profiles    []string       `yaml:"profiles"`
	Ports       []string       `yaml:"ports"`
	NetworkMode string         `yaml:"network_mode"`
	Command     composeCommand `yaml:"command"`
	Environment composeEnv     `yaml:"environment"`
	Volumes     []string       `yaml:"volumes"`
	EnvFile     []string       `yaml:"env_file"`
}

func loadComposeIngress(t *testing.T) map[string]composeIngressService {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	require.NoError(t, err)
	var doc struct {
		Services map[string]composeIngressService `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc.Services
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	require.NoError(t, err)
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// Each bundled ingress is the compose service of its INGRESS name, started only by the profile of
// that name. So `docker compose up` with no profile, the local default, starts no tunnel and no
// proxy: a Telegram-only install runs with none.
func TestCompose_EachIngressIsItsOwnOptInProfile(t *testing.T) {
	services := loadComposeIngress(t)
	require.NotEmpty(t, deploy.Services())
	for _, name := range deploy.Services() {
		svc, ok := services[name]
		if !assert.True(t, ok, "INGRESS=%s needs a compose service named %s", name, name) {
			continue
		}
		assert.Equal(t, []string{name}, svc.Profiles, "%s starts only with COMPOSE_PROFILES=%s", name, name)
		assert.Contains(t, svc.Image, "@sha256:", "%s is pinned by digest", name)
	}
	for name, svc := range services {
		if slices.Contains(deploy.Services(), name) {
			continue
		}
		for _, p := range svc.Profiles {
			assert.False(t, slices.Contains(deploy.Services(), p), "%s must not join the %s ingress profile", name, p)
		}
	}
}

// Every bundled ingress forwards to the app and nothing else: the app's edge decides what is
// public, and the dashboard is never behind an ingress before per-person sign-in (V4-35).
func TestCompose_IngressesReachTheAppOnly(t *testing.T) {
	services := loadComposeIngress(t)

	assert.Contains(t, services["ngrok"].Command, "app:8000")
	assert.Equal(t, "${PUBLIC_URL:-}", services["caddy"].Environment["PUBLIC_URL"])

	caddyfile := repoFile(t, "deploy/Caddyfile")
	assert.Regexp(t, `(?m)^\{\$PUBLIC_URL\} \{\n\treverse_proxy 127\.0\.0\.1:8000\n\}`, caddyfile,
		"Caddy serves PUBLIC_URL and forwards to the app's port")
	assert.Contains(t, services["app"].Ports, "127.0.0.1:8000:8000", "the port Caddy forwards to")
	assert.Contains(t, services["caddy"].Volumes, "./deploy/Caddyfile:/etc/caddy/Caddyfile:ro")

	var serve struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
		AllowFunnel map[string]bool
	}
	require.NoError(t, json.Unmarshal([]byte(repoFile(t, "deploy/tailscale-serve.json")), &serve))
	require.Len(t, serve.Web, 1)
	for host, web := range serve.Web {
		assert.Equal(t, map[string]struct{ Proxy string }{"/": {Proxy: "http://app:8000"}}, web.Handlers)
		assert.True(t, serve.AllowFunnel[host], "Funnel makes it public")
	}
	assert.Contains(t, services["tailscale"].Volumes, "./deploy/tailscale-serve.json:/config/serve.json:ro")

	dashboard := regexp.MustCompile(`dashboard:\d+|:3030|:3000`)
	for _, name := range deploy.Services() {
		for _, arg := range services[name].Command {
			assert.False(t, dashboard.MatchString(arg), "%s must not forward to the dashboard", name)
		}
	}
	assert.NotRegexp(t, dashboard, caddyfile)
	assert.NotRegexp(t, dashboard, repoFile(t, "deploy/tailscale-serve.json"))
}

// The server ingress listens publicly by sharing the host's network, so compose itself still
// publishes nothing beyond loopback (TestDockerComposeNetworkIsolation); the tunnels connect
// outward and publish nothing at all.
func TestCompose_NoIngressPublishesAPort(t *testing.T) {
	services := loadComposeIngress(t)
	assert.Equal(t, "host", services["caddy"].NetworkMode)
	for _, name := range []string{"caddy", "cloudflare", "tailscale"} {
		assert.Empty(t, services[name].Ports, name)
	}
	assert.Equal(t, []string{"127.0.0.1:4040:4040"}, services["ngrok"].Ports, "only ngrok's inspection page, on loopback")
}

// Managed services: compose stops hard-coding the infrastructure addresses. Each service that
// loads .env takes the database, cache and queue address from EXTERNAL_* when set, and falls back
// to the bundled container.
func TestCompose_InfrastructureAddressesCanPointAtManagedServices(t *testing.T) {
	services := loadComposeIngress(t)
	want := map[string]string{
		"DATABASE_URL": "${EXTERNAL_DATABASE_URL:-postgres://",
		"REDIS_URL":    "${EXTERNAL_REDIS_URL:-redis://redis:6379/0}",
		"RABBITMQ_URL": "${EXTERNAL_RABBITMQ_URL:-amqp://",
	}
	checked := 0
	for name, svc := range services {
		if len(svc.EnvFile) == 0 {
			continue
		}
		checked++
		for key, prefix := range want {
			assert.True(t, strings.HasPrefix(svc.Environment[key], prefix), "%s: %s=%s", name, key, svc.Environment[key])
		}
	}
	assert.GreaterOrEqual(t, checked, 4, "app, transfer, migrate and audit-worker")

	template := readRepoFile(t, ".env.example")
	for _, key := range []string{"EXTERNAL_DATABASE_URL", "EXTERNAL_REDIS_URL", "EXTERNAL_RABBITMQ_URL"} {
		assert.Contains(t, template, key+"=", ".env.example must document %s", key)
	}
}

// The settings PUBLIC_URL replaced are gone from compose and from the settings template, which
// documents the new ones and every ingress instead.
func TestCompose_ReplacedSettingsAreGone(t *testing.T) {
	compose := repoFile(t, "docker-compose.yml")
	template := readRepoFile(t, ".env.example")
	for _, key := range replacedEnvKeys() {
		assert.NotContains(t, compose, key, "docker-compose.yml still uses %s", key)
		assert.NotRegexp(t, regexp.MustCompile(`(?m)^#?\s*`+key+`=`), template, ".env.example still offers %s", key)
	}
	for _, key := range []string{"INGRESS=", "PUBLIC_URL=", "COMPOSE_PROFILES="} {
		assert.Contains(t, template, key)
	}
	for _, in := range deploy.Ingresses() {
		assert.Contains(t, template, string(in), ".env.example must list INGRESS=%s", in)
	}
	for _, key := range []string{"CLOUDFLARE_TUNNEL_TOKEN", "TAILSCALE_AUTHKEY", "TAILSCALE_HOSTNAME", "NGROK_AUTHTOKEN"} {
		assert.Contains(t, compose, "${"+key, "compose reads %s", key)
		assert.Contains(t, template, key+"=", ".env.example must document %s", key)
	}
}
