package worker

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"syncopa/internal/task"
)

// TaskReport captures the outcome of processing a single task.
type TaskReport struct {
	Action       task.Action
	Source       string
	Destination  string
	Bytes        int64
	Hash         string
	StartedAt    time.Time
	Duration     time.Duration
	BatchEntries []task.CopyBatchEntry
}

// CompletedAt returns when the task finished.
func (tr TaskReport) CompletedAt() time.Time {
	if tr.StartedAt.IsZero() {
		return time.Time{}
	}
	return tr.StartedAt.Add(tr.Duration)
}

// Report aggregates task execution details for a sync run.
type Report struct {
	StartedAt   time.Time
	CompletedAt time.Time

	totalBytes int64
	copies     []TaskReport
	deletes    []TaskReport
}

// ReportSnapshot captures a serializable representation of a Report so it can
// be persisted and reconstructed later.
type ReportSnapshot struct {
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	TotalBytes  int64        `json:"total_bytes"`
	Copies      []TaskReport `json:"copies"`
	Deletes     []TaskReport `json:"deletes"`
}

func newReport() *Report {
	return &Report{StartedAt: time.Now()}
}

// NewReport creates an empty report. It is primarily used by distributed
// orchestrators that collect task results from remote agents.
func NewReport() *Report {
	return newReport()
}

func (r *Report) add(res *TaskReport) {
	if res == nil {
		return
	}
	switch res.Action {
	case task.ActionCopy, task.ActionCopyBatch:
		r.totalBytes += res.Bytes
		r.copies = append(r.copies, cloneTaskReport(*res))
	case task.ActionDelete:
		r.deletes = append(r.deletes, cloneTaskReport(*res))
	}
}

func (r *Report) markComplete() {
	if r.CompletedAt.IsZero() {
		r.CompletedAt = time.Now()
	}
	sort.Slice(r.copies, func(i, j int) bool {
		return r.copies[i].Destination < r.copies[j].Destination
	})
	sort.Slice(r.deletes, func(i, j int) bool {
		return r.deletes[i].Destination < r.deletes[j].Destination
	})
}

// Finalize freezes the report, computing derived statistics and marking it as
// complete.
func (r *Report) Finalize() {
	r.markComplete()
}

// Record inserts a task report into the aggregate. It mirrors the behaviour of
// the internal add helper but is exported for distributed orchestrators that
// receive task outcomes from remote agents.
func (r *Report) Record(res *TaskReport) {
	r.add(res)
}

// Duration returns the total time spent for the run.
func (r *Report) Duration() time.Duration {
	if r.CompletedAt.IsZero() {
		return 0
	}
	if r.StartedAt.IsZero() {
		return 0
	}
	return r.CompletedAt.Sub(r.StartedAt)
}

// AverageSpeedBytes returns the average throughput in bytes per second.
func (r *Report) AverageSpeedBytes() float64 {
	dur := r.Duration()
	if dur <= 0 {
		return 0
	}
	return float64(r.totalBytes) / dur.Seconds()
}

// ShortSummary returns a compact textual report of the run.
func (r *Report) ShortSummary() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Short Summary Report")
	fmt.Fprintln(&b, strings.Repeat("=", len("Short Summary Report")))
	fmt.Fprintf(&b, "Start: %s\n", r.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "End: %s\n", r.CompletedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Duration: %s\n", r.Duration())
	fmt.Fprintf(&b, "Files copied: %d\n", r.copiedFileCount())
	fmt.Fprintf(&b, "Files deleted: %d\n", len(r.deletes))
	fmt.Fprintf(&b, "Bytes copied: %s\n", formatBytes(r.totalBytes))
	fmt.Fprintf(&b, "Average speed: %s/s\n", formatBytesPerSecond(r.AverageSpeedBytes()))

	if len(r.copies) > 0 {
		fmt.Fprintln(&b, "\nFiles transferred:")
		for _, copy := range r.copies {
			fmt.Fprintf(&b, "- %s (sha256=%s, %s in %s, %s/s)\n",
				copy.Destination,
				copy.Hash,
				formatBytes(copy.Bytes),
				copy.Duration,
				formatBytesPerSecond(speedFromCopy(copy)))
			if len(copy.BatchEntries) > 0 {
				for _, entry := range copy.BatchEntries {
					fmt.Fprintf(&b, "    • %s (%s)\n", entry.Destination, formatBytes(entry.Size))
				}
			}
		}
	}
	return b.String()
}

func speedFromCopy(copy TaskReport) float64 {
	if copy.Duration <= 0 {
		return 0
	}
	return float64(copy.Bytes) / copy.Duration.Seconds()
}

// CopyCount returns the number of copy operations recorded.
func (r *Report) CopyCount() int {
	return len(r.copies)
}

// DeleteCount returns the number of delete operations recorded.
func (r *Report) DeleteCount() int {
	return len(r.deletes)
}

// TotalBytes returns the sum of bytes copied during the run.
func (r *Report) TotalBytes() int64 {
	return r.totalBytes
}

// Copies returns a snapshot of the recorded copy reports.
func (r *Report) Copies() []TaskReport {
	res := make([]TaskReport, len(r.copies))
	for i, tr := range r.copies {
		res[i] = cloneTaskReport(tr)
	}
	return res
}

// Deletes returns a snapshot of the recorded delete reports.
func (r *Report) Deletes() []TaskReport {
	res := make([]TaskReport, len(r.deletes))
	for i, tr := range r.deletes {
		res[i] = cloneTaskReport(tr)
	}
	return res
}

func (r *Report) copiedFileCount() int {
	total := 0
	for _, c := range r.copies {
		total += copyCount(c)
	}
	return total
}

func copyCount(tr TaskReport) int {
	if tr.Action == task.ActionCopyBatch {
		if len(tr.BatchEntries) == 0 {
			return 0
		}
		return len(tr.BatchEntries)
	}
	return 1
}

func cloneTaskReport(src TaskReport) TaskReport {
	dup := src
	if len(src.BatchEntries) > 0 {
		entries := make([]task.CopyBatchEntry, len(src.BatchEntries))
		copy(entries, src.BatchEntries)
		dup.BatchEntries = entries
	}
	return dup
}

func formatBytes(value int64) string {
	if value < 0 {
		return fmt.Sprintf("-%s", formatBytes(-value))
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	exp := int(math.Log(float64(value)) / math.Log(unit))
	if exp > 4 {
		exp = 4
	}
	divisor := math.Pow(unit, float64(exp))
	return fmt.Sprintf("%.2f %ciB", float64(value)/divisor, "KMGT"[exp-1])
}

func formatBytesPerSecond(value float64) string {
	if value <= 0 {
		return "0 B"
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%.2f B", value)
	}
	exp := int(math.Log(value) / math.Log(unit))
	if exp > 4 {
		exp = 4
	}
	divisor := math.Pow(unit, float64(exp))
	return fmt.Sprintf("%.2f %ciB", value/divisor, "KMGT"[exp-1])
}
