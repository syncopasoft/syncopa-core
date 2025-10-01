package scanner

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
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

// Options control the behaviour of Scan.
type Options struct {
	// BatchThreshold groups files whose size is <= the threshold into a
	// single batch task. A value <= 0 disables batching.
	BatchThreshold int64
	// BatchMaxFiles caps how many files can be grouped into a single batch.
	// A value <= 0 means unlimited.
	BatchMaxFiles int
	// BatchMaxBytes caps the total bytes contained in a single batch. A
	// value <= 0 means unlimited.
	BatchMaxBytes int64
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
func Scan(src, dst string, includeDir bool, mode Mode, opts Options, tasks chan<- task.Task) error {
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

	batcher := newCopyBatcher(opts)

	for _, key := range srcFileKeys {
		srcMeta := srcFiles[key]
		dstPath := filepath.Join(cleanDst, key)
		dstMeta, exists := dstFiles[key]
		if !exists {
			if err := batcher.Add(srcMeta.Path, dstPath, srcMeta.Info, tasks); err != nil {
				return err
			}
			continue
		}
		if shouldCopy(srcMeta.Info, dstMeta.Info) {
			if err := batcher.Add(srcMeta.Path, dstPath, srcMeta.Info, tasks); err != nil {
				return err
			}
		}
	}

	if err := batcher.Flush(tasks); err != nil {
		return err
	}

	switch mode {
	case ModeMirror:
		enqueueMirrorDeletes(cleanDst, dstFiles, dstDirs, srcFiles, srcDirs, tasks)
	case ModeSync:
		if err := enqueueSyncTasks(cleanSrc, cleanDst, base, includeDir, srcFiles, dstFiles, srcFileKeys, dstFileKeys, batcher, tasks); err != nil {
			return err
		}
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

func enqueueSyncTasks(cleanSrc, cleanDst, base string, includeDir bool, srcFiles, dstFiles map[string]fileMeta, srcKeys, dstKeys []string, batcher *copyBatcher, tasks chan<- task.Task) error {
	for _, key := range dstKeys {
		dstMeta := dstFiles[key]
		if _, ok := srcFiles[key]; ok {
			continue
		}
		srcPath, ok := srcPathForKey(key, cleanSrc, base, includeDir)
		if !ok {
			continue
		}
		if err := batcher.Add(dstMeta.Path, srcPath, dstMeta.Info, tasks); err != nil {
			return err
		}
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
			if err := batcher.Add(dstMeta.Path, srcPath, dstMeta.Info, tasks); err != nil {
				return err
			}
		}
	}

	return batcher.Flush(tasks)
}

type copyBatcher struct {
	opts       Options
	buf        bytes.Buffer
	tw         *tar.Writer
	entries    []task.CopyBatchEntry
	totalBytes int64
}

func newCopyBatcher(opts Options) *copyBatcher {
	return &copyBatcher{opts: opts}
}

func (b *copyBatcher) enabled() bool {
	return b != nil && b.opts.BatchThreshold > 0
}

func (b *copyBatcher) canAdd(size int64) bool {
	if !b.enabled() {
		return false
	}
	if b.opts.BatchMaxFiles > 0 && len(b.entries) >= b.opts.BatchMaxFiles {
		return false
	}
	if b.opts.BatchMaxBytes > 0 && b.totalBytes+size > b.opts.BatchMaxBytes {
		return false
	}
	return true
}

func (b *copyBatcher) reachedLimits() bool {
	if !b.enabled() {
		return false
	}
	if b.opts.BatchMaxFiles > 0 && len(b.entries) >= b.opts.BatchMaxFiles {
		return true
	}
	if b.opts.BatchMaxBytes > 0 && b.totalBytes >= b.opts.BatchMaxBytes {
		return true
	}
	return false
}

func (b *copyBatcher) Add(src, dst string, info fs.FileInfo, tasks chan<- task.Task) error {
	if !b.enabled() {
		tasks <- task.Task{Action: task.ActionCopy, Src: src, Dst: dst}
		return nil
	}
	if info == nil || info.Size() > b.opts.BatchThreshold {
		if err := b.Flush(tasks); err != nil {
			return err
		}
		tasks <- task.Task{Action: task.ActionCopy, Src: src, Dst: dst}
		return nil
	}
	if !b.canAdd(info.Size()) {
		if err := b.Flush(tasks); err != nil {
			return err
		}
	}
	if b.tw == nil {
		b.tw = tar.NewWriter(&b.buf)
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		b.reset()
		return err
	}
	header.Name = fmt.Sprintf("file-%d", len(b.entries))
	if err := b.tw.WriteHeader(header); err != nil {
		b.reset()
		return err
	}
	if _, err := io.Copy(b.tw, f); err != nil {
		b.reset()
		return err
	}

	b.entries = append(b.entries, task.CopyBatchEntry{Source: src, Destination: dst, Size: info.Size()})
	b.totalBytes += info.Size()

	if b.reachedLimits() {
		return b.Flush(tasks)
	}
	return nil
}

func (b *copyBatcher) Flush(tasks chan<- task.Task) error {
	if !b.enabled() {
		return nil
	}
	if len(b.entries) == 0 {
		b.reset()
		return nil
	}
	if b.tw != nil {
		if err := b.tw.Close(); err != nil {
			return err
		}
	}
	archive := make([]byte, b.buf.Len())
	copy(archive, b.buf.Bytes())
	entries := make([]task.CopyBatchEntry, len(b.entries))
	copy(entries, b.entries)

	src := ""
	dst := ""
	if len(entries) > 0 {
		src = entries[0].Source
		dst = entries[0].Destination
	}

	tasks <- task.Task{Action: task.ActionCopyBatch, Src: src, Dst: dst, Batch: &task.CopyBatchPayload{Entries: entries, Archive: archive}}
	b.reset()
	return nil
}

func (b *copyBatcher) reset() {
	if b == nil {
		return
	}
	b.entries = nil
	b.totalBytes = 0
	b.buf.Reset()
	b.tw = nil
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
