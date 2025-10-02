//go:build !enterprise

package cli

import (
	"fmt"

	"syncopa/internal/worker"
)

func handleSyncReportOutput(report *worker.Report, cfg SyncConfig, _ string, _ string) error {
	if report == nil {
		return fmt.Errorf("nil report")
	}
	if cfg.PrintSummary {
		fmt.Println(report.ShortSummary())
	}
	return nil
}
