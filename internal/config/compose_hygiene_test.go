package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// composeEnv accepts both compose environment forms: a map, or a list of "KEY=value".
type composeEnv map[string]string

func (e *composeEnv) UnmarshalYAML(n *yaml.Node) error {
	out := composeEnv{}
	switch n.Kind {
	case yaml.MappingNode:
		var m map[string]string
		if err := n.Decode(&m); err != nil {
			return err
		}
		for k, v := range m {
			out[k] = v
		}
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		for _, kv := range l {
			k, v, _ := strings.Cut(kv, "=")
			out[k] = v
		}
	}
	*e = out
	return nil
}

// composeCommand accepts both compose command forms: a string or a list.
type composeCommand []string

func (c *composeCommand) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*c = strings.Fields(n.Value)
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*c = l
	return nil
}

type composeService struct {
	Image       string         `yaml:"image"`
	Command     composeCommand `yaml:"command"`
	Profiles    []string       `yaml:"profiles"`
	Environment composeEnv     `yaml:"environment"`
	EnvFile     []string       `yaml:"env_file"`
}

func loadCompose(t *testing.T) (string, map[string]composeService) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	require.NoError(t, err)
	var doc struct {
		Services map[string]composeService `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return string(data), doc.Services
}

// P3-5: no infrastructure details committed in the compose file.
func TestCompose_NoCommittedNgrokDomain(t *testing.T) {
	raw, services := loadCompose(t)
	assert.NotContains(t, raw, "ngrok-free.dev", "a real ngrok domain must come from NGROK_DOMAIN, not a committed default")
	ngrok, ok := services["ngrok"]
	require.True(t, ok)
	var urlArg string
	for _, a := range ngrok.Command {
		if strings.HasPrefix(a, "--url=") {
			urlArg = a
		}
	}
	assert.Equal(t, "--url=${NGROK_DOMAIN:-}", urlArg, "no fallback domain")
}

// Third-party images are pinned to an immutable digest (no floating :latest).
func TestCompose_ThirdPartyToolImagesPinned(t *testing.T) {
	_, services := loadCompose(t)
	for _, name := range []string{"ngrok", "pgadmin"} {
		img := services[name].Image
		assert.Contains(t, img, "@sha256:", "%s must be pinned by digest: %s", name, img)
		assert.NotContains(t, img, ":latest", "%s must not float on :latest", name)
	}
}

// Containers must reach infrastructure by its compose service name. The .env URLs are for
// services run on the host (`go run`, usually localhost), so compose must never interpolate
// them into a container: `${RABBITMQ_URL:-...}` would hand a container "localhost", i.e. itself.
// Credentials still come from .env via the *_USER/*_PASS components.
func TestCompose_ContainerURLsTargetServiceNames(t *testing.T) {
	_, services := loadCompose(t)
	wantHost := map[string]string{
		"RABBITMQ_URL": "@rabbitmq:5672/",
		"REDIS_URL":    "redis://redis:6379/",
		"DATABASE_URL": "@postgres:5432/",
	}
	checked := 0
	for name, svc := range services {
		for key, host := range wantHost {
			v, ok := svc.Environment[key]
			if !ok {
				continue
			}
			checked++
			assert.NotContains(t, v, "${"+key, "%s: %s must not be taken from the host .env (it may point at localhost)", name, key)
			assert.Contains(t, v, host, "%s: %s must target the compose service", name, key)
		}
	}
	assert.GreaterOrEqual(t, checked, 8, "expected the Go services to declare their infrastructure URLs")
}

// BL-7: every external image — compose services and Dockerfile bases — is pinned to an immutable
// digest (with a readable tag), so a rebuild can never silently pick up different bits.
// Dependabot (P5-4) bumps tag+digest pairs deliberately.
func TestAllExternalImagesArePinnedByDigest(t *testing.T) {
	_, services := loadCompose(t)
	for name, svc := range services {
		if svc.Image == "" {
			continue // built from a Dockerfile, checked below
		}
		assert.Contains(t, svc.Image, "@sha256:", "compose service %s: %s", name, svc.Image)
	}

	for _, df := range []string{"Dockerfile", filepath.Join("dashboard", "Dockerfile")} {
		data, err := os.ReadFile(filepath.Join("..", "..", df))
		require.NoError(t, err)
		stages := map[string]bool{}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.EqualFold(f[0], "FROM") {
				continue
			}
			ref := f[1]
			if len(f) >= 4 && strings.EqualFold(f[2], "AS") {
				stages[strings.ToLower(f[3])] = true
			}
			if stages[strings.ToLower(ref)] && !strings.Contains(ref, "@") {
				continue // FROM <earlier stage>
			}
			assert.Contains(t, ref, "@sha256:", "%s: FROM %s", df, ref)
		}
	}
}

// BL-4: env_file injects the host .env into the container, including host-side *_URL values that
// point at localhost. Any service that loads it must override all three, even if it does not use
// them today, so a future code path can never silently read "localhost" (the DP-1 trap).
func TestCompose_EnvFileServicesOverrideEveryInfraURL(t *testing.T) {
	_, services := loadCompose(t)
	checked := 0
	for name, svc := range services {
		if len(svc.EnvFile) == 0 {
			continue
		}
		checked++
		for _, key := range []string{"RABBITMQ_URL", "REDIS_URL", "DATABASE_URL"} {
			_, ok := svc.Environment[key]
			assert.True(t, ok, "%s loads env_file but does not override %s", name, key)
		}
	}
	assert.GreaterOrEqual(t, checked, 4, "gateway, orchestrator, migrate and audit-worker load .env")
}

// pgAdmin is opt-in and has no default credentials.
func TestCompose_PgAdminOptInWithoutDefaultPassword(t *testing.T) {
	raw, services := loadCompose(t)
	pg := services["pgadmin"]
	assert.Equal(t, []string{"tools"}, pg.Profiles, "pgAdmin must only start with --profile tools")
	assert.Equal(t, "${PGADMIN_DEFAULT_PASSWORD:-}", pg.Environment["PGADMIN_DEFAULT_PASSWORD"], "no default password")
	assert.NotContains(t, raw, "PGADMIN_DEFAULT_PASSWORD:-admin")
}
