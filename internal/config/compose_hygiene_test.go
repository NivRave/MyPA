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

// pgAdmin is opt-in and has no default credentials.
func TestCompose_PgAdminOptInWithoutDefaultPassword(t *testing.T) {
	raw, services := loadCompose(t)
	pg := services["pgadmin"]
	assert.Equal(t, []string{"tools"}, pg.Profiles, "pgAdmin must only start with --profile tools")
	assert.Equal(t, "${PGADMIN_DEFAULT_PASSWORD:-}", pg.Environment["PGADMIN_DEFAULT_PASSWORD"], "no default password")
	assert.NotContains(t, raw, "PGADMIN_DEFAULT_PASSWORD:-admin")
}
