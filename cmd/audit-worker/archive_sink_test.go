package main

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The local sink writes gzip JSONL atomically (temp file + rename) and round-trips every field,
// including the P2-9 tool summary.
func TestLocalGzipSink_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	sink := localGzipSink{dir: dir}
	in := []models.AuditSession{
		{ID: "s1", UserID: "u1", UserPrompt: "add dentist", ToolSummary: "create_calendar_event: id=evt_1", StartTime: time.Unix(100, 0).UTC()},
		{ID: "s2", UserID: "u2", UserPrompt: "תודה", StartTime: time.Unix(200, 0).UTC()},
	}

	loc, err := sink.Write("audit_archive_123", in)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "audit_archive_123.jsonl.gz"), loc)

	f, err := os.Open(loc)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	dec := json.NewDecoder(gz)
	var out []models.AuditSession
	for dec.More() {
		var s models.AuditSession
		require.NoError(t, dec.Decode(&s))
		out = append(out, s)
	}
	assert.Equal(t, in, out)

	entries, _ := os.ReadDir(dir)
	assert.Len(t, entries, 1, "only the final archive remains (no temp file)")
}

// A write that cannot complete leaves no partial archive behind.
func TestLocalGzipSink_FailureLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	// A directory occupying the final name makes the rename fail.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "x.jsonl.gz"), 0o755))
	_, err := localGzipSink{dir: dir}.Write("x", []models.AuditSession{{ID: "s"}})
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(dir, "x.jsonl.gz.tmp"))
	assert.True(t, os.IsNotExist(statErr), "temp file must be cleaned up")
}
