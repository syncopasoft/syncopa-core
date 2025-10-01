package scanner

import (
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
		if err := Scan(srcDir, dstDir, false, ModeUpdate, tasksCh); err != nil {
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
		if err := Scan(srcDir, dstDir, false, ModeSync, tasksCh); err != nil {
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
