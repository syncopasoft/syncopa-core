package scanner

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"syncopa/internal/task"
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
	// AutoTuneBatching enables automatic configuration of the batching
	// parameters based on the observed source files. Manual values above
	// take precedence when provided.
	AutoTuneBatching bool
}

// ParseMode converts a string into a Mode value.
func ParseMode(s string) (Mode, error) {
	m, ok := modeNames[strings.ToLower(s)]
	if !ok {
		return ModeUpdate, fmt.Errorf("unknown mode %q", s)
	}
	return m, nil
}

func tuneBatchingOptions(opts Options, files map[string]fileMeta) Options {
	if !opts.AutoTuneBatching {
		return opts
	}
	if opts.BatchThreshold > 0 || opts.BatchMaxFiles > 0 || opts.BatchMaxBytes > 0 {
		// Manual overrides always win.
		return opts
	}

	sizes := make([]int64, 0, len(files))
	for _, meta := range files {
		if meta.Info == nil {
			continue
		}
		size := meta.Info.Size()
		if size < 0 {
			continue
		}
		sizes = append(sizes, size)
	}
	if len(sizes) == 0 {
		return opts
	}

	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })

	const smallFileCutoff = int64(512 * 1024)
	small := make([]int64, 0, len(sizes))
	for _, size := range sizes {
		if size <= smallFileCutoff {
			small = append(small, size)
		}
	}
	if len(small) < 4 {
		// Too few small files to benefit from batching.
		return opts
	}

	totalSmall := int64(0)
	for _, size := range small {
		totalSmall += size
	}
	avgSmall := totalSmall / int64(len(small))
	if avgSmall <= 0 {
		avgSmall = 1
	}

	median := percentileInt64(small, 0.5)
	if median <= 0 {
		median = avgSmall
	}
	p90 := percentileInt64(small, 0.9)
	if p90 <= 0 {
		p90 = median
	}

	threshold := p90
	minThreshold := int64(4 * 1024)
	if threshold < 2*median {
		threshold = 2 * median
	}
	if threshold < minThreshold {
		threshold = minThreshold
	}
	if threshold > smallFileCutoff {
		threshold = smallFileCutoff
	}

	// Aim for batches around a few megabytes so the worker only needs to
	// unpack a handful of archives per second.
	targetBatchBytes := avgSmall * 64
	if targetBatchBytes < 1<<20 {
		targetBatchBytes = 1 << 20
	}
	if targetBatchBytes > 8<<20 {
		targetBatchBytes = 8 << 20
	}
	if targetBatchBytes < threshold*4 {
		targetBatchBytes = threshold * 4
	}

	maxFiles := int(targetBatchBytes / avgSmall)
	if maxFiles < 8 {
		maxFiles = 8
	}
	if maxFiles > 512 {
		maxFiles = 512
	}

	opts.BatchThreshold = threshold
	opts.BatchMaxFiles = maxFiles
	opts.BatchMaxBytes = targetBatchBytes
	return opts
}

func percentileInt64(data []int64, pct float64) int64 {
	if len(data) == 0 {
		return 0
	}
	if pct <= 0 {
		return data[0]
	}
	if pct >= 1 {
		return data[len(data)-1]
	}
	idx := int(math.Ceil(pct*float64(len(data)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(data) {
		idx = len(data) - 1
	}
	return data[idx]
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

	tunedOpts := tuneBatchingOptions(opts, srcFiles)
	batcher := newCopyBatcher(tunedOpts)

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
	copyBuf    []byte
}

func newCopyBatcher(opts Options) *copyBatcher {
	b := &copyBatcher{opts: opts}
	if opts.BatchMaxBytes > 0 {
		// Reserve enough capacity for the expected payload plus some headroom
		// for tar headers while keeping memory usage bounded.
		reserve := opts.BatchMaxBytes + int64(opts.BatchMaxFiles+1)*512
		const maxReserve = int64(16 << 20) // 16 MiB cap to avoid large allocations
		if reserve > maxReserve {
			reserve = maxReserve
		}
		if reserve > 0 {
			b.buf.Grow(int(reserve))
		}
	}
	if opts.BatchThreshold > 0 {
		bufSize := opts.BatchThreshold / 2
		if bufSize < 32*1024 {
			bufSize = 32 * 1024
		}
		if bufSize > 256*1024 {
			bufSize = 256 * 1024
		}
		b.copyBuf = make([]byte, bufSize)
	}
	return b
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
	if b.copyBuf != nil {
		if _, err := io.CopyBuffer(b.tw, f, b.copyBuf); err != nil {
			b.reset()
			return err
		}
	} else {
		if _, err := io.Copy(b.tw, f); err != nil {
			b.reset()
			return err
		}
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
