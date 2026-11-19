package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type composeToolService struct {
	Profiles   []string `yaml:"profiles"`
	Entrypoint []string `yaml:"entrypoint"`
	Restart    string   `yaml:"restart"`
	Volumes    []string `yaml:"volumes"`
	Ports      []string `yaml:"ports"`
}

func loadComposeTools(t *testing.T) map[string]composeToolService {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	require.NoError(t, err)
	var doc struct {
		Services map[string]composeToolService `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	return doc.Services
}

// V4-43: the restore-and-migrate tool is a one-shot service. `docker compose up` must never
// start it, it must never restart, and it needs both things a backup is made of: the backup
// directory and the archive files.
func TestCompose_TransferToolIsOptInAndSeesBackupsAndArchives(t *testing.T) {
	svc, ok := loadComposeTools(t)["transfer"]
	require.True(t, ok, "docker-compose.yml must define the transfer service")

	assert.Equal(t, []string{"tools"}, svc.Profiles, "transfer must be opt-in: `up` must not run a restore tool")
	assert.Equal(t, "no", svc.Restart)
	assert.Equal(t, []string{"/usr/local/bin/mypa", "transfer"}, svc.Entrypoint,
		"`docker compose run --rm transfer export` must run `mypa transfer export`")
	assert.Contains(t, svc.Volumes, "./backups:/backups")
	assert.Contains(t, svc.Volumes, "audit_archives:/app/archives", "an import writes archive files, so the mount is read-write")
	assert.Empty(t, svc.Ports, "the tool listens on nothing")
}

// A backup is the database dump plus the archive files, and the app takes the backups (V4-01:
// the orchestrator is part of the app). It only ever reads the archives: the audit worker stays
// their single writer.
func TestCompose_AppReadsArchivesForBackups(t *testing.T) {
	services := loadComposeTools(t)
	assert.Contains(t, services["app"].Volumes, "audit_archives:/app/archives:ro")
	assert.Contains(t, services["audit-worker"].Volumes, "audit_archives:/app/archives")
}
