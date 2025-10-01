package scanner

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"os"

	"migratool/internal/task"
)

func TestScanDeterministicOrderUpdate(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	files := []string{"c.txt", "a.txt", "b.txt", filepath.Join("nested", "file.txt")}
	for _, rel := range files {
		writeTestFile(t, srcDir, rel, rel)
	}

	expectedPaths := append([]string(nil), files...)
	sort.Strings(expectedPaths)
	expected := make([]string, len(expectedPaths))
	for i, rel := range expectedPaths {
		expected[i] = directionKey("src", rel)
	}

	var previousOrder []string
	for i := 0; i < 3; i++ {
		tasksCh := make(chan task.Task, len(expected))
		if err := Scan(srcDir, dstDir, false, ModeUpdate, Options{}, tasksCh); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		close(tasksCh)

		got := readTaskOrder(tasksCh, srcDir, dstDir)
		if !reflect.DeepEqual(expected, got) {
			t.Fatalf("unexpected task order: got %v want %v", got, expected)
		}
		if i == 0 {
			previousOrder = got
			continue
		}
		if !reflect.DeepEqual(previousOrder, got) {
			t.Fatalf("non-deterministic order: got %v want %v", got, previousOrder)
		}
	}
}

func TestScanEmitsCopyBatch(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	files := map[string]string{
		"a.txt":        "alpha",
		"nested/b.txt": "bravo",
		"nested/c.txt": "charlie",
	}
	for rel, contents := range files {
		writeTestFile(t, srcDir, rel, contents)
	}

	tasksCh := make(chan task.Task, len(files))
	opts := Options{BatchThreshold: 1024, BatchMaxFiles: 10, BatchMaxBytes: 4096}
	if err := Scan(srcDir, dstDir, false, ModeUpdate, opts, tasksCh); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	close(tasksCh)

	var tasks []task.Task
	for task := range tasksCh {
		tasks = append(tasks, task)
	}

	if len(tasks) != 1 {
		t.Fatalf("expected a single batch task, got %d", len(tasks))
	}
	batchTask := tasks[0]
	if batchTask.Action != task.ActionCopyBatch {
		t.Fatalf("unexpected action: got %v want %v", batchTask.Action, task.ActionCopyBatch)
	}
	if batchTask.Batch == nil {
		t.Fatal("batch payload missing")
	}
	if len(batchTask.Batch.Entries) != len(files) {
		t.Fatalf("unexpected entry count: got %d want %d", len(batchTask.Batch.Entries), len(files))
	}
	if len(batchTask.Batch.Archive) == 0 {
		t.Fatal("batch archive is empty")
	}

	seen := make(map[string]task.CopyBatchEntry)
	for _, entry := range batchTask.Batch.Entries {
		seen[entry.Destination] = entry
	}

	for rel, contents := range files {
		dstPath := filepath.Join(dstDir, rel)
		entry, ok := seen[dstPath]
		if !ok {
			t.Fatalf("missing entry for %s", dstPath)
		}
		if entry.Size != int64(len(contents)) {
			t.Fatalf("unexpected entry size for %s: got %d want %d", dstPath, entry.Size, len(contents))
		}
	}
}

func TestScanDeterministicOrderSync(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Files that should be copied from src -> dst.
	srcOnly := []string{"gamma.txt", "alpha.txt"}
	for _, rel := range srcOnly {
		writeTestFile(t, srcDir, rel, rel)
	}

	// Files that should be copied from dst -> src.
	dstOnly := []string{"delta.txt", "beta.txt"}
	for _, rel := range dstOnly {
		writeTestFile(t, dstDir, rel, rel)
	}

	// Shared file where destination is newer and should copy back to source.
	shared := "shared.txt"
	sharedSrcPath := writeTestFile(t, srcDir, shared, "src")
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(sharedSrcPath, oldTime, oldTime); err != nil {
		t.Fatalf("failed to adjust src time: %v", err)
	}
	sharedDstPath := writeTestFile(t, dstDir, shared, "dst")
	newTime := oldTime.Add(2 * time.Hour)
	if err := os.Chtimes(sharedDstPath, newTime, newTime); err != nil {
		t.Fatalf("failed to adjust dst time: %v", err)
	}

	expected := []string{
		directionKey("src", "alpha.txt"),
		directionKey("src", "gamma.txt"),
		directionKey("dst", "beta.txt"),
		directionKey("dst", "delta.txt"),
		directionKey("dst", "shared.txt"),
	}

	var previousOrder []string
	for i := 0; i < 3; i++ {
		tasksCh := make(chan task.Task, len(expected))
		if err := Scan(srcDir, dstDir, false, ModeSync, Options{}, tasksCh); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		close(tasksCh)

		got := readTaskOrder(tasksCh, srcDir, dstDir)
		if !reflect.DeepEqual(expected, got) {
			t.Fatalf("unexpected task order: got %v want %v", got, expected)
		}
		if i == 0 {
			previousOrder = got
			continue
		}
		if !reflect.DeepEqual(previousOrder, got) {
			t.Fatalf("non-deterministic order: got %v want %v", got, previousOrder)
		}
	}
}

func TestTuneBatchingOptionsAutoBatch(t *testing.T) {
	files := make(map[string]fileMeta)
	now := time.Now()
	for i := 0; i < 64; i++ {
		size := int64(2*1024 + (i%4)*512)
		name := fmt.Sprintf("file-%d", i)
		files[name] = fileMeta{Path: name, Info: stubFileInfo{name: name, size: size, modTime: now}}
	}

	opts := tuneBatchingOptions(Options{AutoTuneBatching: true}, files)
	if opts.BatchThreshold <= 0 {
		t.Fatalf("expected positive threshold, got %d", opts.BatchThreshold)
	}
	if opts.BatchMaxFiles <= 0 {
		t.Fatalf("expected positive max files, got %d", opts.BatchMaxFiles)
	}
	if opts.BatchMaxBytes <= 0 {
		t.Fatalf("expected positive max bytes, got %d", opts.BatchMaxBytes)
	}
	if opts.BatchThreshold < 1024 || opts.BatchThreshold > 512*1024 {
		t.Fatalf("unexpected threshold %d", opts.BatchThreshold)
	}
}

func TestTuneBatchingOptionsLargeFilesDisabled(t *testing.T) {
	files := make(map[string]fileMeta)
	now := time.Now()
	for i := 0; i < 8; i++ {
		size := int64(2*1024*1024 + int64(i)*1024)
		name := fmt.Sprintf("large-%d", i)
		files[name] = fileMeta{Path: name, Info: stubFileInfo{name: name, size: size, modTime: now}}
	}

	opts := tuneBatchingOptions(Options{AutoTuneBatching: true}, files)
	if opts.BatchThreshold != 0 {
		t.Fatalf("expected threshold to remain zero, got %d", opts.BatchThreshold)
	}
	if opts.BatchMaxFiles != 0 {
		t.Fatalf("expected max files to remain zero, got %d", opts.BatchMaxFiles)
	}
	if opts.BatchMaxBytes != 0 {
		t.Fatalf("expected max bytes to remain zero, got %d", opts.BatchMaxBytes)
	}
}

type stubFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (s stubFileInfo) Name() string       { return s.name }
func (s stubFileInfo) Size() int64        { return s.size }
func (s stubFileInfo) Mode() fs.FileMode  { return s.mode }
func (s stubFileInfo) ModTime() time.Time { return s.modTime }
func (s stubFileInfo) IsDir() bool        { return false }
func (s stubFileInfo) Sys() any           { return nil }

func directionKey(direction, rel string) string {
	return direction + ":" + filepath.ToSlash(rel)
}

func readTaskOrder(tasks <-chan task.Task, srcRoot, dstRoot string) []string {
	cleanSrc := filepath.Clean(srcRoot)
	cleanDst := filepath.Clean(dstRoot)

	var order []string
	for task := range tasks {
		switch {
		case strings.HasPrefix(task.Dst, cleanDst):
			rel, err := filepath.Rel(cleanDst, task.Dst)
			if err != nil {
				panic(err)
			}
			order = append(order, directionKey("src", rel))
		case strings.HasPrefix(task.Dst, cleanSrc):
			rel, err := filepath.Rel(cleanSrc, task.Dst)
			if err != nil {
				panic(err)
			}
			order = append(order, directionKey("dst", rel))
		default:
			panic("unexpected task destination: " + task.Dst)
		}
	}
	return order
}

func writeTestFile(t *testing.T, root, rel, contents string) string {
	t.Helper()

	fullPath := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("failed to create dirs for %s: %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", fullPath, err)
	}
	return fullPath
}
