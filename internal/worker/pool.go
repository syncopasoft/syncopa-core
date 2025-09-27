package worker

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"migratool/internal/task"
)

// Pool copies files using a set of workers.
type Pool struct {
	Workers int
	Verbose bool
}

// New creates a new worker pool.
func New(workers int, verbose bool) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{Workers: workers, Verbose: verbose}
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
		return copyFile(t.Src, t.Dst)
	case task.ActionDelete:
		if p.Verbose {
			log.Printf("delete %s", t.Dst)
		}
		return deletePath(t.Dst)
	default:
		return fmt.Errorf("unknown task action: %d", t.Action)
	}
}

func copyFile(src, dst string) error {
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

	_, err = io.Copy(out, in)
	return err
}

func deletePath(path string) error {
	// os.RemoveAll succeeds even if the path does not exist.
	return os.RemoveAll(path)
}
