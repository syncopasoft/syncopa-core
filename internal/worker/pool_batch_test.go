package worker

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/syncopasoft/syncopa-core/internal/task"
)

func TestHandleTaskCopyBatch(t *testing.T) {
	dir := t.TempDir()

	files := []struct {
		name    string
		content string
	}{
		{"first.txt", "alpha"},
		{filepath.Join("nested", "second.txt"), "bravo"},
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	entries := make([]task.CopyBatchEntry, len(files))
	for i, file := range files {
		header := &tar.Header{
			Name:     file.name,
			Mode:     0o644,
			Size:     int64(len(file.content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("failed to write header: %v", err)
		}
		if _, err := tw.Write([]byte(file.content)); err != nil {
			t.Fatalf("failed to write file contents: %v", err)
		}
		entries[i] = task.CopyBatchEntry{
			Source:      filepath.Join("/src", file.name),
			Destination: filepath.Join(dir, file.name),
			Size:        int64(len(file.content)),
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}

	payload := &task.CopyBatchPayload{Entries: entries, Archive: buf.Bytes()}
	pool := New(1, false, 0)

	report, err := pool.handleTask(task.Task{Action: task.ActionCopyBatch, Batch: payload})
	if err != nil {
		t.Fatalf("handleTask returned error: %v", err)
	}
	if report.Action != task.ActionCopyBatch {
		t.Fatalf("unexpected action: got %v want %v", report.Action, task.ActionCopyBatch)
	}
	if len(report.BatchEntries) != len(entries) {
		t.Fatalf("unexpected batch entry count: got %d want %d", len(report.BatchEntries), len(entries))
	}
	expectedBytes := int64(0)
	for _, entry := range entries {
		expectedBytes += entry.Size
	}
	if report.Bytes != expectedBytes {
		t.Fatalf("unexpected bytes: got %d want %d", report.Bytes, expectedBytes)
	}

	wantHash := sha256.Sum256(payload.Archive)
	if report.Hash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("unexpected hash: got %s want %s", report.Hash, hex.EncodeToString(wantHash[:]))
	}

	for _, entry := range entries {
		data, err := os.ReadFile(entry.Destination)
		if err != nil {
			t.Fatalf("failed to read %s: %v", entry.Destination, err)
		}
		rel, err := filepath.Rel(dir, entry.Destination)
		if err != nil {
			t.Fatalf("failed to derive relative path: %v", err)
		}
		rel = filepath.Clean(rel)
		var expected string
		for _, file := range files {
			if filepath.Clean(file.name) == rel {
				expected = file.content
				break
			}
		}
		if string(data) != expected {
			t.Fatalf("unexpected file contents for %s: got %q want %q", entry.Destination, string(data), expected)
		}
	}
}
