package worker

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"
	"testing"
	"time"

	"migratool/internal/task"
)

func TestReportWriteCSV(t *testing.T) {
	base := time.Date(2024, 5, 20, 15, 4, 5, 0, time.UTC)
	copyStart := base.Add(150 * time.Millisecond)
	copyDuration := 2 * time.Second
	deleteStart := base.Add(1250 * time.Millisecond)
	deleteDuration := 1500 * time.Millisecond

	report := &Report{StartedAt: base}
	report.add(&TaskReport{
		Action:      task.ActionCopy,
		Source:      "/src/a.txt",
		Destination: "/dst/a.txt",
		Bytes:       2048,
		Hash:        "abc123",
		StartedAt:   copyStart,
		Duration:    copyDuration,
	})
	report.add(&TaskReport{
		Action:      task.ActionDelete,
		Destination: "/dst/old.txt",
		StartedAt:   deleteStart,
		Duration:    deleteDuration,
	})
	report.CompletedAt = base.Add(3 * time.Second)
	report.markComplete()

	var buf bytes.Buffer
	if err := report.WriteCSV(&buf); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	reader := csv.NewReader(bytes.NewReader(buf.Bytes()))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse csv output: %v", err)
	}

	wantRecords := [][]string{
		{"summary", "start", base.Format(time.RFC3339)},
		{"summary", "end", report.CompletedAt.Format(time.RFC3339)},
		{"summary", "duration_seconds", "3.000"},
		{"summary", "copied_files", "1"},
		{"summary", "deleted_files", "1"},
		{"summary", "bytes_copied", "2048"},
		{"summary", "average_bytes_per_second", "682.67"},
		{"action", "source", "destination", "bytes", "hash", "duration_seconds", "started_at", "completed_at", "speed_bytes_per_sec"},
		{"copy", "/src/a.txt", "/dst/a.txt", "2048", "abc123", "2.000", copyStart.Format(time.RFC3339), copyStart.Add(copyDuration).Format(time.RFC3339), "1024.00"},
		{"delete", "", "/dst/old.txt", "", "", "1.500", deleteStart.Format(time.RFC3339), deleteStart.Add(deleteDuration).Format(time.RFC3339), ""},
	}

	if len(records) != len(wantRecords) {
		t.Fatalf("unexpected record count: got %d want %d\noutput:\n%s", len(records), len(wantRecords), buf.String())
	}
	for i := range wantRecords {
		if strings.Join(records[i], "|") != strings.Join(wantRecords[i], "|") {
			t.Fatalf("record %d mismatch:\n got %v\nwant %v", i, records[i], wantRecords[i])
		}
	}
}

func TestReportWritePDF(t *testing.T) {
	base := time.Date(2024, 2, 1, 9, 30, 0, 0, time.UTC)
	report := &Report{StartedAt: base}
	report.add(&TaskReport{Action: task.ActionCopy, Destination: "/dst/a.txt", Bytes: 4096})
	report.add(&TaskReport{Action: task.ActionCopy, Destination: "/dst/b.txt", Bytes: 1024})
	report.add(&TaskReport{Action: task.ActionDelete, Destination: "/dst/remove.txt"})
	report.CompletedAt = base.Add(90 * time.Second)
	report.markComplete()

	var buf bytes.Buffer
	if err := report.WritePDF(&buf); err != nil {
		t.Fatalf("WritePDF returned error: %v", err)
	}

	pdfData := buf.Bytes()
	if len(pdfData) == 0 {
		t.Fatal("WritePDF produced no output")
	}
	if !bytes.HasPrefix(pdfData, []byte("%PDF-")) {
		t.Fatalf("PDF does not start with header: %q", pdfData[:7])
	}
	if !strings.Contains(string(pdfData), "Migration Report") {
		t.Fatalf("expected title to be embedded in PDF output")
	}

	if err := report.WritePDF(badWriter{}); err == nil {
		t.Fatal("expected error when writer fails")
	}
}

type badWriter struct{}

func (badWriter) Write(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
