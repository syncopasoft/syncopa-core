package worker

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"syncopa/internal/task"
)

// WriteCSV serialises the report details into CSV format. The output contains
// summary rows followed by a detailed breakdown of every recorded task. The
// function is deterministic so the resulting file can be diffed or processed
// by spreadsheet tools.
func (r *Report) WriteCSV(w io.Writer) error {
	if r == nil {
		return errors.New("report is nil")
	}
	if w == nil {
		return errors.New("writer is nil")
	}

	writer := csv.NewWriter(w)

	summaryRecords := [][]string{
		{"summary", "start", formatTimestamp(r.StartedAt)},
		{"summary", "end", formatTimestamp(r.CompletedAt)},
		{"summary", "duration_seconds", formatFloat(r.Duration().Seconds(), 3)},
		{"summary", "copied_files", strconv.Itoa(r.copiedFileCount())},
		{"summary", "deleted_files", strconv.Itoa(len(r.deletes))},
		{"summary", "bytes_copied", strconv.FormatInt(r.totalBytes, 10)},
		{"summary", "average_bytes_per_second", formatFloat(r.AverageSpeedBytes(), 2)},
	}
	for _, record := range summaryRecords {
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	if err := writer.Write(nil); err != nil {
		return err
	}

	header := []string{"action", "source", "destination", "bytes", "hash", "duration_seconds", "started_at", "completed_at", "speed_bytes_per_sec"}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, copy := range r.copies {
		record := []string{
			actionLabel(copy.Action),
			copy.Source,
			copy.Destination,
			strconv.FormatInt(copy.Bytes, 10),
			copy.Hash,
			formatFloat(copy.Duration.Seconds(), 3),
			formatTimestamp(copy.StartedAt),
			formatTimestamp(copy.CompletedAt()),
			formatFloat(speedFromCopy(copy), 2),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	for _, del := range r.deletes {
		record := []string{
			actionLabel(task.ActionDelete),
			del.Source,
			del.Destination,
			"",
			"",
			formatFloat(del.Duration.Seconds(), 3),
			formatTimestamp(del.StartedAt),
			formatTimestamp(del.CompletedAt()),
			"",
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	writer.Flush()
	return writer.Error()
}

// WritePDF produces a standalone PDF summary of the report including a brief
// description of the run and simple ASCII bar charts for the largest
// transfers. The generated PDF is intentionally lightweight and avoids third
// party dependencies so it can be produced in constrained environments.
func (r *Report) WritePDF(w io.Writer) error {
	if r == nil {
		return errors.New("report is nil")
	}
	if w == nil {
		return errors.New("writer is nil")
	}

	lines := r.pdfLines()
	content := buildPDFContent(lines)

	objects := []pdfObject{
		{ID: 1, Body: []byte("<< /Type /Catalog /Pages 2 0 R >>")},
		{ID: 2, Body: []byte("<< /Type /Pages /Count 1 /Kids [3 0 R] >>")},
		{ID: 3, Body: []byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")},
		{ID: 4, Body: buildPDFStream(content)},
		{ID: 5, Body: []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")},
	}

	buf := bytes.NewBufferString("%PDF-1.4\n")
	offsets := make(map[int]int, len(objects)+1)

	for _, obj := range objects {
		offsets[obj.ID] = buf.Len()
		fmt.Fprintf(buf, "%d 0 obj\n%s\nendobj\n", obj.ID, obj.Body)
	}

	xrefOffset := buf.Len()
	total := len(objects) + 1
	fmt.Fprintf(buf, "xref\n0 %d\n", total)
	fmt.Fprintf(buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(buf, "%010d %05d n \n", offsets[i], 0)
	}
	fmt.Fprintf(buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%EOF\n", total, xrefOffset)

	_, err := w.Write(buf.Bytes())
	return err
}

func (r *Report) pdfLines() []string {
	const title = "Migration Report"

	lines := []string{
		title,
		strings.Repeat("=", len(title)),
		fmt.Sprintf("Start: %s", formatTimestamp(r.StartedAt)),
		fmt.Sprintf("End: %s", formatTimestamp(r.CompletedAt)),
		fmt.Sprintf("Duration: %s", r.Duration()),
		"",
		fmt.Sprintf("Files copied: %d", r.copiedFileCount()),
		fmt.Sprintf("Files deleted: %d", len(r.deletes)),
		fmt.Sprintf("Bytes copied: %s", formatBytes(r.totalBytes)),
		fmt.Sprintf("Average speed: %s/s", formatBytesPerSecond(r.AverageSpeedBytes())),
	}

	copies := append([]TaskReport(nil), r.copies...)
	sort.Slice(copies, func(i, j int) bool {
		if copies[i].Bytes == copies[j].Bytes {
			return copies[i].Destination < copies[j].Destination
		}
		return copies[i].Bytes > copies[j].Bytes
	})
	if len(copies) > 5 {
		copies = copies[:5]
	}

	if len(copies) > 0 {
		lines = append(lines, "", "Top Transfers", "--------------")
		maxBytes := copies[0].Bytes
		if maxBytes <= 0 {
			maxBytes = 1
		}
		for _, c := range copies {
			label := truncateText(relPath(c.Destination), 40)
			bar := renderBar(c.Bytes, maxBytes, 40)
			lines = append(lines, fmt.Sprintf("%-40s | %-40s %s", label, bar, formatBytes(c.Bytes)))
		}
	} else {
		lines = append(lines, "", "No file copy operations were recorded.")
	}

	if len(r.deletes) > 0 {
		lines = append(lines, "", "Deleted Paths", "-------------")
		limit := len(r.deletes)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			lines = append(lines, fmt.Sprintf("- %s", truncateText(relPath(r.deletes[i].Destination), 60)))
		}
		if len(r.deletes) > limit {
			lines = append(lines, fmt.Sprintf("- ... %d more", len(r.deletes)-limit))
		}
	}

	return lines
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return t.Format(time.RFC3339)
}

func actionLabel(action task.Action) string {
	switch action {
	case task.ActionCopy:
		return "copy"
	case task.ActionCopyBatch:
		return "copy_batch"
	case task.ActionDelete:
		return "delete"
	default:
		return fmt.Sprintf("action_%d", action)
	}
}

func formatFloat(v float64, precision int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, v)
}

func renderBar(value, max int64, width int) string {
	if width <= 0 {
		return ""
	}
	if max <= 0 {
		return ""
	}
	ratio := float64(value) / float64(max)
	if ratio < 0 {
		ratio = 0
	}
	length := int(math.Round(ratio * float64(width)))
	if value > 0 && length == 0 {
		length = 1
	}
	if length > width {
		length = width
	}
	return strings.Repeat("#", length)
}

func truncateText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

func relPath(path string) string {
	if path == "" {
		return path
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cleaned); err == nil && !strings.HasPrefix(rel, "../") {
			return filepath.Join("~", rel)
		}
	}
	return cleaned
}

func buildPDFContent(lines []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("BT\n/F1 12 Tf\n14 TL\n72 800 Td\n")
	for i, line := range lines {
		if i > 0 {
			buf.WriteString("T*\n")
		}
		fmt.Fprintf(&buf, "(%s) Tj\n", escapePDFText(line))
	}
	buf.WriteString("ET\n")
	return buf.Bytes()
}

func buildPDFStream(content []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<< /Length %d >>\nstream\n", len(content))
	buf.Write(content)
	buf.WriteString("endstream")
	return buf.Bytes()
}

type pdfObject struct {
	ID   int
	Body []byte
}

func escapePDFText(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)")
	return replacer.Replace(s)
}
