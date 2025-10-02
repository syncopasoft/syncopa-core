//go:build enterprise

package worker

import (
	"testing"

	"syncopa/internal/task"
)

func TestReportSnapshotRoundTrip(t *testing.T) {
	report := NewReport()
	report.StartedAt = report.StartedAt.Add(-123)
	report.Record(&TaskReport{Action: task.ActionCopy, Source: "src/a", Destination: "dst/a", Bytes: 1024})
	report.Record(&TaskReport{Action: task.ActionDelete, Destination: "dst/b"})
	report.Finalize()

	snap := report.Snapshot()
	restored := ReportFromSnapshot(snap)

	if restored.StartedAt != report.StartedAt {
		t.Fatalf("expected StartedAt %v, got %v", report.StartedAt, restored.StartedAt)
	}
	if restored.CompletedAt.IsZero() {
		t.Fatalf("restored report missing completion time")
	}
	if restored.TotalBytes() != report.TotalBytes() {
		t.Fatalf("expected total bytes %d, got %d", report.TotalBytes(), restored.TotalBytes())
	}
	if restored.CopyCount() != report.CopyCount() {
		t.Fatalf("expected copy count %d, got %d", report.CopyCount(), restored.CopyCount())
	}
	if restored.DeleteCount() != report.DeleteCount() {
		t.Fatalf("expected delete count %d, got %d", report.DeleteCount(), restored.DeleteCount())
	}

	// Ensure the snapshot is detached; mutating the restored report should not
	// affect the original.
	restored.Record(&TaskReport{Action: task.ActionCopy, Source: "src/c", Destination: "dst/c"})
	if restored.CopyCount() == report.CopyCount() {
		t.Fatalf("expected copy counts to diverge after mutation")
	}
}
