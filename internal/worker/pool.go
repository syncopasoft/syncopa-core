package worker

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"migratool/internal/task"
)

// Pool copies files using a set of workers.
type Pool struct {
	Workers int
	Verbose bool
	// BandwidthLimit limits the number of bytes per second used when copying files.
	// A value <= 0 disables throttling.
	BandwidthLimit int64
}

// New creates a new worker pool.
func New(workers int, verbose bool, bandwidthLimit int64) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{Workers: workers, Verbose: verbose, BandwidthLimit: bandwidthLimit}
}

// Run starts the worker pool and processes tasks from the channel.
func (p *Pool) Run(tasks <-chan task.Task) error {
	var wg sync.WaitGroup
	errs := make(chan error, p.Workers)
	for i := 0; i < p.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				if err := p.handleTask(t); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Pool) handleTask(t task.Task) error {
	switch t.Action {
	case task.ActionCopy:
		if p.Verbose {
			log.Printf("copy %s -> %s", t.Src, t.Dst)
		}
		return p.copyFile(t.Src, t.Dst)
	case task.ActionDelete:
		if p.Verbose {
			log.Printf("delete %s", t.Dst)
		}
		return deletePath(t.Dst)
	default:
		return fmt.Errorf("unknown task action: %d", t.Action)
	}
}

func (p *Pool) copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = copyWithBandwidth(out, in, p.BandwidthLimit)
	return err
}

func copyWithBandwidth(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit <= 0 {
		return io.Copy(dst, src)
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
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			expectedDuration := time.Duration(float64(written+int64(n)) * float64(time.Second) / float64(limit))
			elapsed := time.Since(start)
			if expectedDuration > elapsed {
				time.Sleep(expectedDuration - elapsed)
			}

			wn, writeErr := dst.Write(buf[:n])
			written += int64(wn)
			if writeErr != nil {
				return written, writeErr
			}
			if wn != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func deletePath(path string) error {
	// os.RemoveAll succeeds even if the path does not exist.
	return os.RemoveAll(path)
}
