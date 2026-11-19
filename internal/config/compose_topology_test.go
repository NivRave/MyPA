package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// V4-01: the app is one binary with roles. These tests read docker-compose.yml and the Dockerfile
// as committed, so they prove what `docker compose up` starts without starting anything.

type composeTopologyService struct {
	Image       string                       `yaml:"image"`
	Build       *struct{ Target string }     `yaml:"build"`
	Command     composeCommand               `yaml:"command"`
	Entrypoint  composeCommand               `yaml:"entrypoint"`
	Profiles    []string                     `yaml:"profiles"`
	Restart     string                       `yaml:"restart"`
	Ports       []string                     `yaml:"ports"`
	Environment composeEnv                   `yaml:"environment"`
	DependsOn   map[string]map[string]string `yaml:"depends_on"`
}

func loadComposeTopology(t *testing.T) map[string]composeTopologyService {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	require.NoError(t, err)
	var doc struct {
		Services map[string]composeTopologyService `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.Services)
	return doc.Services
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The "done when" of V4-01: `docker compose up` runs one app container plus the audit worker, the
// dashboard and infrastructure. `up` starts every service without a profile; of those, the ones
// that keep running (restart is not "no") and are built from this repository are exactly the app,
// the audit worker and the dashboard. Everything else that keeps running is a third-party image,
// and the only one-shot job is the schema migration, which runs the app image.
func TestCompose_UpRunsOneAppPlusAuditWorkerDashboardAndInfrastructure(t *testing.T) {
	services := loadComposeTopology(t)

	built := map[string]bool{}
	infrastructure := map[string]bool{}
	oneShot := map[string]bool{}
	for name, svc := range services {
		if len(svc.Profiles) > 0 {
			continue // opt-in, never started by `docker compose up`
		}
		switch {
		case svc.Restart == "no":
			oneShot[name] = true
		case svc.Build != nil:
			built[name] = true
		default:
			require.NotEmpty(t, svc.Image, "%s is neither built here nor a pinned image", name)
			infrastructure[name] = true
		}
	}

	assert.Equal(t, []string{"app", "audit-worker", "dashboard"}, sortedKeys(built),
		"`docker compose up` must run one app container plus the audit worker and the dashboard")
	assert.Equal(t, []string{"migrate"}, sortedKeys(oneShot), "the only one-shot job `up` runs is the schema migration")
	assert.Equal(t, "app", services["migrate"].Build.Target, "the migration runs the app image")
	assert.Equal(t, []string{"ngrok", "postgres", "rabbitmq", "redis"}, sortedKeys(infrastructure),
		"everything else `up` starts is infrastructure")

	for _, gone := range []string{"proxy", "gateway", "orchestrator"} {
		_, present := services[gone]
		assert.False(t, present, "V4-01 merged %s into the app", gone)
	}

	app := services["app"]
	assert.Equal(t, "app", app.Build.Target)
	assert.Equal(t, []string{"127.0.0.1:8000:8000"}, app.Ports, "the app keeps the old proxy port, on the loopback address only")
	assert.Empty(t, app.Command, "the app runs its default role, all")
}

// The other half of the "done when": the dashboard talks to the app directly, and nothing in the
// compose file still points at a service that V4-01 removed.
func TestCompose_DashboardTalksToTheAppDirectly(t *testing.T) {
	services := loadComposeTopology(t)

	dashboard := services["dashboard"]
	assert.Equal(t, "http://app:8000/graphql", dashboard.Environment["GRAPHQL_ENDPOINT"])
	assert.Equal(t, "http://localhost:8000", dashboard.Environment["NEXT_PUBLIC_APP_URL"])
	assert.Contains(t, dashboard.DependsOn, "app")

	ngrok := services["ngrok"]
	assert.Contains(t, ngrok.Command, "app:8000", "the tunnel reaches the app")
	assert.Contains(t, ngrok.DependsOn, "app")

	oldHost := regexp.MustCompile(`\b(proxy|gateway|orchestrator):\d+`)
	for name, svc := range services {
		for key, value := range svc.Environment {
			assert.False(t, oldHost.MatchString(value), "%s sets %s=%s, a service V4-01 removed", name, key, value)
		}
		for _, arg := range svc.Command {
			assert.False(t, oldHost.MatchString(arg), "%s runs %q, a service V4-01 removed", name, arg)
		}
		for dep := range svc.DependsOn {
			_, exists := services[dep]
			assert.True(t, exists, "%s depends on %s, which does not exist", name, dep)
		}
	}
}

// One binary, built once: the Dockerfile has one app target, and the three command packages it
// replaced are gone.
func TestDockerfile_BuildsOneAppBinary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	require.NoError(t, err)
	dockerfile := string(data)

	var targets []string
	for _, m := range regexp.MustCompile(`(?m)^FROM \S+ AS (\S+)\s*$`).FindAllStringSubmatch(dockerfile, -1) {
		targets = append(targets, m[1])
	}
	sort.Strings(targets)
	assert.Equal(t, []string{"app", "audit-worker", "build-app", "build-audit-worker", "deps"}, targets)
	assert.Contains(t, dockerfile, "COPY cmd/mypa/ cmd/mypa/")
	assert.Contains(t, dockerfile, `ENTRYPOINT ["/usr/local/bin/mypa"]`)

	for _, gone := range []string{"proxy", "gateway", "orchestrator"} {
		_, err := os.Stat(filepath.Join("..", "..", "cmd", gone))
		assert.True(t, os.IsNotExist(err), "cmd/%s must be gone: it is part of cmd/mypa now", gone)
	}
}

// Instructions the app gives in chat or prints (the /restore reply, /doctor, the restore tool) may
// only name services that exist. Before V4-01 they named proxy, gateway and orchestrator; a
// command naming a missing service fails for the person following it.
func TestCompose_CommandsInCodeNameExistingServices(t *testing.T) {
	services := loadComposeTopology(t)
	command := regexp.MustCompile("docker compose (?:stop|start|restart|logs) ([a-z0-9 -]+)")

	root := filepath.Join("..", "..")
	searched := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			searched++
			for _, m := range command.FindAllStringSubmatch(string(data), -1) {
				for _, word := range strings.Fields(m[1]) {
					if strings.HasPrefix(word, "-") {
						continue
					}
					rel, _ := filepath.Rel(root, path)
					_, exists := services[word]
					assert.True(t, exists, "%s tells the user to run %q, but there is no %s service", filepath.ToSlash(rel), m[0], word)
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.Greater(t, searched, 100, "the search must have covered the source tree")
}
