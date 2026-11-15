package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nivik/mypa/internal/models"
)

// ArchiveSink stores a batch of expired audit sessions durably. The audit worker deletes the
// sessions from Postgres only after Write succeeds. Local disk is the only implementation today;
// an object-storage sink (S3/R2/GCS/Azure Blob) can implement the same interface once an
// off-machine target is chosen (shared decision with P3-4 backups).
type ArchiveSink interface {
	// Write stores sessions under name and returns where they were written.
	Write(name string, sessions []models.AuditSession) (location string, err error)
}

// localGzipSink writes <dir>/<name>.jsonl.gz (P2-9). Conversation text compresses roughly 5-10x.
// The file is written to a temp name, fsynced, then renamed, so a crash never leaves a truncated
// archive under the final name.
type localGzipSink struct {
	dir string
}

func (s localGzipSink) Write(name string, sessions []models.AuditSession) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create archive directory: %w", err)
	}
	final := filepath.Join(s.dir, name+".jsonl.gz")
	tmp := final + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("failed to create archive file: %w", err)
	}
	fail := func(stage string, err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("failed to %s archive: %w", stage, err)
	}

	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	for _, sess := range sessions {
		if err := enc.Encode(sess); err != nil {
			return fail("encode", err)
		}
	}
	if err := gz.Close(); err != nil {
		return fail("compress", err)
	}
	if err := f.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("failed to close archive: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("failed to finalize archive: %w", err)
	}
	return final, nil
}
