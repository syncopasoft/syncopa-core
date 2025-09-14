package worker

import (
	"io"
	"os"
	"path/filepath"
	"sync"

	"migratool/internal/task"
)

// Pool copies files using a set of workers.
type Pool struct {
	Workers int
}

// New creates a new worker pool.
func New(workers int) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{Workers: workers}
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
				if err := copyFile(t.Src, t.Dst); err != nil {
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
