package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// P3-3: every compose service rotates its logs (json-file, bounded size and count), so container
// logs cannot fill the disk.
func TestCompose_EveryServiceHasLogRotation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	require.NoError(t, err)

	var doc struct {
		Services map[string]struct {
			Logging struct {
				Driver  string            `yaml:"driver"`
				Options map[string]string `yaml:"options"`
			} `yaml:"logging"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc)) // yaml.v3 resolves anchors/aliases
	require.NotEmpty(t, doc.Services)

	for name, svc := range doc.Services {
		assert.Equal(t, "json-file", svc.Logging.Driver, "%s: logging driver", name)
		assert.NotEmpty(t, svc.Logging.Options["max-size"], "%s: max-size", name)
		assert.NotEmpty(t, svc.Logging.Options["max-file"], "%s: max-file", name)
	}
}
