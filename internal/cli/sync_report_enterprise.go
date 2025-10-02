//go:build enterprise

package cli

import (
	"fmt"

	"syncopa/internal/worker"
)

func handleSyncReportOutput(report *worker.Report, cfg SyncConfig, pdfPath, csvPath string) error {
	if report == nil {
		return fmt.Errorf("nil report")
	}
	if cfg.PrintSummary {
		fmt.Println(report.ShortSummary())
	}
	if cfg.PrintVerboseReport {
		if cfg.PrintSummary {
			fmt.Println()
		}
		fmt.Println(report.VerboseReport())
	}
	if cfg.EnableReportFlags {
		if err := writeReportFile(pdfPath, report.WritePDF); err != nil {
			return fmt.Errorf("failed to write PDF report: %w", err)
		}
		if err := writeReportFile(csvPath, report.WriteCSV); err != nil {
			return fmt.Errorf("failed to write CSV report: %w", err)
		}
	}
	return nil
}
