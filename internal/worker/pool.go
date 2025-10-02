package worker

import (
	"sync"

	"syncopa/internal/task"
)

// Pool copies files using a set of workers.
type Pool struct {
	Workers int
	Verbose bool
	// BandwidthLimit limits the number of bytes per second used when copying files.
	// A value <= 0 disables throttling.
	BandwidthLimit int64

	executor *Executor
}

// New creates a new worker pool.
func New(workers int, verbose bool, bandwidthLimit int64) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{
		Workers:        workers,
		Verbose:        verbose,
		BandwidthLimit: bandwidthLimit,
		executor:       NewExecutor(verbose, bandwidthLimit),
	}
}

// Run starts the worker pool and processes tasks from the channel.
func (p *Pool) Run(tasks <-chan task.Task) (*Report, error) {
	report := newReport()
	results := make(chan *TaskReport, p.Workers)
	var collector sync.WaitGroup
	collector.Add(1)
	go func() {
		defer collector.Done()
		for res := range results {
			report.add(res)
		}
	}()

	errs := make(chan error, p.Workers)
	var wg sync.WaitGroup

	// Ensure any runtime adjustments to the public fields are reflected in the executor.
	p.executor.Verbose = p.Verbose
	p.executor.BandwidthLimit = p.BandwidthLimit

	for i := 0; i < p.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				res, err := p.executor.RunTask(t)
				if err != nil {
					errs <- err
					continue
				}
				if res != nil {
					results <- res
				}
			}
		}()
	}

	wg.Wait()
	close(results)
	collector.Wait()
	close(errs)

	var firstErr error
	for err := range errs {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	report.Finalize()

	if firstErr != nil {
		return report, firstErr
	}
	return report, nil
}

func (p *Pool) handleTask(t task.Task) (*TaskReport, error) {
	return p.executor.RunTask(t)
}
