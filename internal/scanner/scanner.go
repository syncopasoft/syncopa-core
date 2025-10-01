package scanner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"migratool/internal/task"
)

// Mode controls how source and destination are reconciled.
type Mode int

const (
	// ModeUpdate copies new or updated files from source to destination.
	ModeUpdate Mode = iota
	// ModeMirror makes the destination match the source by removing extras.
	ModeMirror
	// ModeSync keeps both source and destination in sync.
	ModeSync
)

var modeNames = map[string]Mode{
	"update": ModeUpdate,
	"mirror": ModeMirror,
	"sync":   ModeSync,
}

// ParseMode converts a string into a Mode value.
func ParseMode(s string) (Mode, error) {
	m, ok := modeNames[strings.ToLower(s)]
	if !ok {
		return ModeUpdate, fmt.Errorf("unknown mode %q", s)
	}
	return m, nil
}

// Scan walks the source and destination directories and emits tasks based on
// the selected mode.
func Scan(src, dst string, includeDir bool, mode Mode, tasks chan<- task.Task) error {
	if src == "" || dst == "" {
		return errors.New("src and dst required")
	}

	cleanSrc := filepath.Clean(src)
	cleanDst := filepath.Clean(dst)

	base := ""
	dstRoot := cleanDst
	if includeDir {
		base = filepath.Base(cleanSrc)
		if base == string(os.PathSeparator) || base == "." {
			includeDir = false
		} else {
			dstRoot = filepath.Join(cleanDst, base)
		}
	}

	srcSnap, err := snapshot(cleanSrc)
	if err != nil {
		return err
	}
	dstSnap, err := snapshot(dstRoot)
	if err != nil {
		return err
	}

	srcFiles := make(map[string]fileMeta, len(srcSnap.Files))
	srcDirs := make(map[string]fileMeta, len(srcSnap.Dirs))
	srcFileKeys := make([]string, 0, len(srcSnap.Files))
	for rel, meta := range srcSnap.Files {
		key := withPrefix(base, rel, includeDir)
		srcFiles[key] = meta
		srcFileKeys = append(srcFileKeys, key)
	}
	sort.Strings(srcFileKeys)
	for rel, meta := range srcSnap.Dirs {
		key := withPrefix(base, rel, includeDir)
		srcDirs[key] = meta
	}

	dstFiles := make(map[string]fileMeta, len(dstSnap.Files))
	dstDirs := make(map[string]fileMeta, len(dstSnap.Dirs))
	dstFileKeys := make([]string, 0, len(dstSnap.Files))
	for rel, meta := range dstSnap.Files {
		key := withPrefix(base, rel, includeDir)
		dstFiles[key] = meta
		dstFileKeys = append(dstFileKeys, key)
	}
	sort.Strings(dstFileKeys)
	for rel, meta := range dstSnap.Dirs {
		key := withPrefix(base, rel, includeDir)
		dstDirs[key] = meta
	}

	for _, key := range srcFileKeys {
		srcMeta := srcFiles[key]
		dstPath := filepath.Join(cleanDst, key)
		dstMeta, exists := dstFiles[key]
		if !exists {
			tasks <- task.Task{Action: task.ActionCopy, Src: srcMeta.Path, Dst: dstPath}
			continue
		}
		if shouldCopy(srcMeta.Info, dstMeta.Info) {
			tasks <- task.Task{Action: task.ActionCopy, Src: srcMeta.Path, Dst: dstPath}
		}
	}

	switch mode {
	case ModeMirror:
		enqueueMirrorDeletes(cleanDst, dstFiles, dstDirs, srcFiles, srcDirs, tasks)
	case ModeSync:
		enqueueSyncTasks(cleanSrc, cleanDst, base, includeDir, srcFiles, dstFiles, srcFileKeys, dstFileKeys, tasks)
	}

	return nil
}

func enqueueMirrorDeletes(cleanDst string, dstFiles, dstDirs, srcFiles, srcDirs map[string]fileMeta, tasks chan<- task.Task) {
	for key, dstMeta := range dstFiles {
		if _, ok := srcFiles[key]; ok {
			continue
		}
		tasks <- task.Task{Action: task.ActionDelete, Dst: dstMeta.Path}
	}

	missingDirs := make([]string, 0, len(dstDirs))
	for key := range dstDirs {
		if _, ok := srcDirs[key]; ok {
			continue
		}
		missingDirs = append(missingDirs, key)
	}
	sort.Slice(missingDirs, func(i, j int) bool {
		return len(missingDirs[i]) > len(missingDirs[j])
	})
	for _, rel := range missingDirs {
		tasks <- task.Task{Action: task.ActionDelete, Dst: filepath.Join(cleanDst, rel)}
	}
}

func enqueueSyncTasks(cleanSrc, cleanDst, base string, includeDir bool, srcFiles, dstFiles map[string]fileMeta, srcKeys, dstKeys []string, tasks chan<- task.Task) {
	for _, key := range dstKeys {
		dstMeta := dstFiles[key]
		if _, ok := srcFiles[key]; ok {
			continue
		}
		srcPath, ok := srcPathForKey(key, cleanSrc, base, includeDir)
		if !ok {
			continue
		}
		tasks <- task.Task{Action: task.ActionCopy, Src: dstMeta.Path, Dst: srcPath}
	}

	for _, key := range srcKeys {
		srcMeta := srcFiles[key]
		dstMeta, ok := dstFiles[key]
		if !ok {
			continue
		}
		if dstMeta.Info.ModTime().After(srcMeta.Info.ModTime()) {
			srcPath, ok := srcPathForKey(key, cleanSrc, base, includeDir)
			if !ok {
				continue
			}
			tasks <- task.Task{Action: task.ActionCopy, Src: dstMeta.Path, Dst: srcPath}
		}
	}
}

func shouldCopy(srcInfo, dstInfo fs.FileInfo) bool {
	if dstInfo == nil {
		return true
	}
	if srcInfo.Size() != dstInfo.Size() {
		return true
	}
	return srcInfo.ModTime().After(dstInfo.ModTime())
}

type fileMeta struct {
	Path string
	Info fs.FileInfo
}

func snapshot(root string) (*snapshotResult, error) {
	res := &snapshotResult{
		Files: make(map[string]fileMeta),
		Dirs:  make(map[string]fileMeta),
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entryInfo, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			res.Dirs[rel] = fileMeta{Path: path, Info: entryInfo}
			return nil
		}
		res.Files[rel] = fileMeta{Path: path, Info: entryInfo}
		return nil
	})
	return res, err
}

type snapshotResult struct {
	Files map[string]fileMeta
	Dirs  map[string]fileMeta
}

func withPrefix(prefix, rel string, include bool) string {
	if !include || prefix == "" {
		return rel
	}
	if rel == "" {
		return prefix
	}
	return filepath.Join(prefix, rel)
}

func srcPathForKey(key, cleanSrc, base string, includeDir bool) (string, bool) {
	if !includeDir || base == "" {
		return filepath.Join(cleanSrc, key), true
	}
	if key == base {
		return cleanSrc, true
	}
	prefix := base + string(os.PathSeparator)
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(key, prefix)
	return filepath.Join(cleanSrc, rel), true
}
