package worker

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/syncopasoft/syncopa-core/internal/task"
)

// Executor processes individual tasks using the same logic as the local worker pool.
type Executor struct {
	// Verbose toggles detailed logging for each action that is performed.
	Verbose bool
	// BandwidthLimit limits the number of bytes per second used when copying files.
	// A value <= 0 disables throttling.
	BandwidthLimit int64
}

// NewExecutor constructs an Executor configured with the supplied options.
func NewExecutor(verbose bool, bandwidthLimit int64) *Executor {
	return &Executor{Verbose: verbose, BandwidthLimit: bandwidthLimit}
}

// RunTask executes a single task and returns a TaskReport describing the outcome.
func (e *Executor) RunTask(t task.Task) (*TaskReport, error) {
	switch t.Action {
	case task.ActionCopy:
		if e.Verbose {
			log.Printf("copy %s -> %s", t.Src, t.Dst)
		}
		start := time.Now()
		bytes, hash, err := e.copyFile(t.Src, t.Dst)
		duration := time.Since(start)
		if err != nil {
			return nil, err
		}
		return &TaskReport{
			Action:      t.Action,
			Source:      t.Src,
			Destination: t.Dst,
			Bytes:       bytes,
			Hash:        hash,
			StartedAt:   start,
			Duration:    duration,
		}, nil
	case task.ActionCopyBatch:
		if t.Batch == nil {
			return nil, fmt.Errorf("copy batch task missing payload")
		}
		if e.Verbose {
			log.Printf("copy batch (%d files)", len(t.Batch.Entries))
		}
		start := time.Now()
		bytesCopied, hash, err := e.copyBatch(t.Batch)
		duration := time.Since(start)
		if err != nil {
			return nil, err
		}
		entries := append([]task.CopyBatchEntry(nil), t.Batch.Entries...)
		destination := fmt.Sprintf("batch of %d files", len(entries))
		source := ""
		if len(entries) > 0 {
			source = entries[0].Source
			destination = fmt.Sprintf("%s (batch of %d files)", entries[0].Destination, len(entries))
		}
		return &TaskReport{
			Action:       t.Action,
			Source:       source,
			Destination:  destination,
			Bytes:        bytesCopied,
			Hash:         hash,
			StartedAt:    start,
			Duration:     duration,
			BatchEntries: entries,
		}, nil
	case task.ActionDelete:
		if e.Verbose {
			log.Printf("delete %s", t.Dst)
		}
		start := time.Now()
		if err := deletePath(t.Dst); err != nil {
			return nil, err
		}
		return &TaskReport{
			Action:      t.Action,
			Destination: t.Dst,
			StartedAt:   start,
			Duration:    time.Since(start),
		}, nil
	default:
		return nil, fmt.Errorf("unknown task action: %d", t.Action)
	}
}

func (e *Executor) copyFile(src, dst string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, "", err
	}
	if e.BandwidthLimit <= 0 {
		if written, hash, used, err := tryZeroCopy(src, dst); err != nil {
			return written, hash, err
		} else if used {
			return written, hash, nil
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, "", err
	}
	defer out.Close()

	written, hash, err := copyWithBandwidth(out, in, e.BandwidthLimit)
	return written, hash, err
}

func (e *Executor) copyBatch(payload *task.CopyBatchPayload) (int64, string, error) {
	if payload == nil {
		return 0, "", fmt.Errorf("batch payload is nil")
	}
	reader := bytes.NewReader(payload.Archive)
	tr := tar.NewReader(reader)
	hashBytes := sha256.Sum256(payload.Archive)

	var totalBytes int64
	for i, entry := range payload.Entries {
		header, err := tr.Next()
		if err != nil {
			return totalBytes, "", fmt.Errorf("reading batch entry %d: %w", i, err)
		}
		if header == nil {
			return totalBytes, "", fmt.Errorf("missing tar header for entry %d", i)
		}
		if entry.Size >= 0 && header.Size != entry.Size {
			// Prefer the metadata from the payload which was derived from the source file.
			header.Size = entry.Size
		}
		if err := os.MkdirAll(filepath.Dir(entry.Destination), 0o755); err != nil {
			return totalBytes, "", err
		}
		out, err := os.Create(entry.Destination)
		if err != nil {
			return totalBytes, "", err
		}
		limited := io.LimitReader(tr, header.Size)
		written, _, copyErr := copyWithBandwidth(out, limited, e.BandwidthLimit)
		closeErr := out.Close()
		totalBytes += written
		if copyErr != nil {
			return totalBytes, "", copyErr
		}
		if closeErr != nil {
			return totalBytes, "", closeErr
		}
	}

	return totalBytes, hex.EncodeToString(hashBytes[:]), nil
}

func copyWithBandwidth(dst io.Writer, src io.Reader, limit int64) (int64, string, error) {
	if limit <= 0 {
		hasher := sha256.New()
		mw := io.MultiWriter(dst, hasher)
		written, err := io.Copy(mw, src)
		if err != nil {
			return written, "", err
		}
		return written, hex.EncodeToString(hasher.Sum(nil)), nil
	}

	bufSize := 32 * 1024
	if limit > 0 && int64(bufSize) > limit {
		bufSize = int(limit)
		if bufSize == 0 {
			bufSize = 1
		}
	}

	buf := make([]byte, bufSize)
	start := time.Now()
	hasher := sha256.New()
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			expectedDuration := time.Duration(float64(written+int64(n)) * float64(time.Second) / float64(limit))
			elapsed := time.Since(start)
			if expectedDuration > elapsed {
				time.Sleep(expectedDuration - elapsed)
			}

			chunk := buf[:n]
			if _, hashErr := hasher.Write(chunk); hashErr != nil {
				return written, "", hashErr
			}
			wn, writeErr := dst.Write(chunk)
			written += int64(wn)
			if writeErr != nil {
				return written, "", writeErr
			}
			if wn != n {
				return written, "", io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, hex.EncodeToString(hasher.Sum(nil)), nil
			}
			return written, "", readErr
		}
	}
}

func deletePath(path string) error {
	// os.RemoveAll succeeds even if the path does not exist.
	return os.RemoveAll(path)
}
