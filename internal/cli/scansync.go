package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"syncopa/internal/scanner"
	"syncopa/internal/task"
	"syncopa/internal/worker"
)

// AutoBatchConfig controls how automatic batching options are exposed to the CLI.
type AutoBatchConfig struct {
	// EnableFlag toggles the presence of the --auto-batch flag.
	EnableFlag bool
	// Default determines the default auto-batching behaviour when the flag is available or omitted.
	Default bool
	// Forced overrides the computed auto-batch value when non-nil.
	Forced *bool
}

// ScanConfig captures the knobs for the scan command wiring.
type ScanConfig struct {
	AutoBatch AutoBatchConfig
}

// SyncConfig captures the knobs for the sync command wiring.
type SyncConfig struct {
	AutoBatch          AutoBatchConfig
	EnableReportFlags  bool
	PrintSummary       bool
	PrintVerboseReport bool
}

// RunScan executes the scan command using the provided arguments and configuration.
func RunScan(args []string, cfg ScanConfig) error {
	scanCmd := flag.NewFlagSet("scan", flag.ExitOnError)
	src := scanCmd.String("src", "", "source directory")
	dst := scanCmd.String("dst", "", "destination directory")
	modeFlag := scanCmd.String("mode", "update", "planning mode: update (one-way copy), mirror (one-way copy + deletes), sync (bidirectional)")
	verbose := scanCmd.Bool("verbose", false, "enable verbose output")
	batchThreshold := scanCmd.Int64("batch-threshold", 0, "maximum file size in bytes eligible for batching (0 disables)")
	batchMaxFiles := scanCmd.Int("batch-max-files", 0, "maximum files per batch task (0 for unlimited)")
	batchMaxBytes := scanCmd.Int64("batch-max-bytes", 0, "maximum total bytes per batch task (0 for unlimited)")
	var autoBatchFlag *bool
	if cfg.AutoBatch.EnableFlag {
		autoBatchFlag = scanCmd.Bool("auto-batch", cfg.AutoBatch.Default, "automatically tune batching parameters based on discovered files")
	}
	help := scanCmd.Bool("help", false, "show help for scan")
	scanCmd.Usage = func() {
		out := scanCmd.Output()
		fmt.Fprintf(out, "Usage: %s scan --src <path> --dst <path> [options]\n", os.Args[0])
		fmt.Fprintln(out, "\nDescription:")
		fmt.Fprintln(out, "  Analyze differences between a source and destination to plan work.")
		fmt.Fprintln(out, "\nModes:")
		fmt.Fprintln(out, "  update  Copy new or modified files from source to destination without touching extras.")
		fmt.Fprintln(out, "  mirror  Copy changes and delete files that only exist at the destination so it mirrors the source.")
		fmt.Fprintln(out, "  sync    Keep both locations in sync by copying newer files in either direction.")
		fmt.Fprintln(out, "")
		scanCmd.PrintDefaults()
	}
	if err := scanCmd.Parse(args); err != nil {
		return err
	}
	if *help {
		scanCmd.Usage()
		return nil
	}
	if *src == "" || *dst == "" {
		return fmt.Errorf("src and dst required")
	}
	includeDir := !HasTrailingSeparator(*src)
	mode, err := scanner.ParseMode(*modeFlag)
	if err != nil {
		return err
	}
	opts := scanner.Options{
		BatchThreshold:   *batchThreshold,
		BatchMaxFiles:    *batchMaxFiles,
		BatchMaxBytes:    *batchMaxBytes,
		AutoTuneBatching: cfg.AutoBatch.Default,
	}
	if cfg.AutoBatch.Forced != nil {
		opts.AutoTuneBatching = *cfg.AutoBatch.Forced
	} else if autoBatchFlag != nil {
		opts.AutoTuneBatching = *autoBatchFlag
	}

	tasks := make(chan task.Task)
	scanErr := make(chan error, 1)
	go func() {
		defer close(tasks)
		scanErr <- scanner.Scan(*src, *dst, includeDir, mode, opts, tasks)
	}()

	for t := range tasks {
		switch t.Action {
		case task.ActionCopy:
			if *verbose {
				fmt.Printf("[copy:%s] %s -> %s\n", *modeFlag, t.Src, t.Dst)
			} else {
				fmt.Printf("%s -> %s\n", t.Src, t.Dst)
			}
		case task.ActionCopyBatch:
			count := 0
			var totalBytes int64
			firstDst := t.Dst
			if t.Batch != nil {
				count = len(t.Batch.Entries)
				if count > 0 && firstDst == "" {
					firstDst = t.Batch.Entries[0].Destination
				}
				for _, entry := range t.Batch.Entries {
					totalBytes += entry.Size
				}
			}
			if *verbose {
				fmt.Printf("[copy-batch:%s] %d files (%d bytes) -> %s\n", *modeFlag, count, totalBytes, firstDst)
			} else {
				fmt.Printf("batch %d files -> %s\n", count, firstDst)
			}
		case task.ActionDelete:
			if *verbose {
				fmt.Printf("[delete:%s] %s\n", *modeFlag, t.Dst)
			} else {
				fmt.Printf("delete %s\n", t.Dst)
			}
		}
	}

	if err := <-scanErr; err != nil {
		return err
	}
	return nil
}

// RunSync executes the sync command using the provided arguments and configuration.
func RunSync(args []string, cfg SyncConfig) error {
	syncCmd := flag.NewFlagSet("sync", flag.ExitOnError)
	src := syncCmd.String("src", "", "source directory")
	dst := syncCmd.String("dst", "", "destination directory")
	workers := syncCmd.Int("workers", 4, "number of workers")
	bandwidth := syncCmd.Int64("bandwidth", 0, "maximum bandwidth in bytes per second when copying (0 for unlimited)")
	modeFlag := syncCmd.String("mode", "update", "sync mode: update (one-way copy), mirror (one-way copy + deletes), sync (bidirectional)")
	verbose := syncCmd.Bool("verbose", false, "enable verbose output")
	batchThreshold := syncCmd.Int64("batch-threshold", 0, "maximum file size in bytes eligible for batching (0 disables)")
	batchMaxFiles := syncCmd.Int("batch-max-files", 0, "maximum files per batch task (0 for unlimited)")
	batchMaxBytes := syncCmd.Int64("batch-max-bytes", 0, "maximum total bytes per batch task (0 for unlimited)")
	var autoBatchFlag *bool
	if cfg.AutoBatch.EnableFlag {
		autoBatchFlag = syncCmd.Bool("auto-batch", cfg.AutoBatch.Default, "automatically tune batching parameters based on discovered files")
	}
	var reportPDF, reportCSV *string
	if cfg.EnableReportFlags {
		reportPDF = syncCmd.String("report-pdf", "", "write a PDF summary report to the specified file")
		reportCSV = syncCmd.String("report-csv", "", "write a detailed CSV report to the specified file")
	}
	help := syncCmd.Bool("help", false, "show help for sync")
	syncCmd.Usage = func() {
		out := syncCmd.Output()
		fmt.Fprintf(out, "Usage: %s sync --src <path> --dst <path> [options]\n", os.Args[0])
		fmt.Fprintln(out, "\nDescription:")
		fmt.Fprintln(out, "  Execute the planned work to copy, update, or delete files so the targets align.")
		fmt.Fprintln(out, "\nModes:")
		fmt.Fprintln(out, "  update  Copy new or modified files from source to destination without removing extras.")
		fmt.Fprintln(out, "  mirror  Copy changes and delete files that only exist at the destination so it mirrors the source.")
		fmt.Fprintln(out, "  sync    Keep both locations in sync by copying newer files in either direction.")
		fmt.Fprintln(out, "")
		syncCmd.PrintDefaults()
	}
	if err := syncCmd.Parse(args); err != nil {
		return err
	}
	if *help {
		syncCmd.Usage()
		return nil
	}
	if *src == "" || *dst == "" {
		return fmt.Errorf("src and dst required")
	}
	includeDir := !HasTrailingSeparator(*src)
	mode, err := scanner.ParseMode(*modeFlag)
	if err != nil {
		return err
	}
	opts := scanner.Options{
		BatchThreshold:   *batchThreshold,
		BatchMaxFiles:    *batchMaxFiles,
		BatchMaxBytes:    *batchMaxBytes,
		AutoTuneBatching: cfg.AutoBatch.Default,
	}
	if cfg.AutoBatch.Forced != nil {
		opts.AutoTuneBatching = *cfg.AutoBatch.Forced
	} else if autoBatchFlag != nil {
		opts.AutoTuneBatching = *autoBatchFlag
	}

	tasks := make(chan task.Task)
	scanErr := make(chan error, 1)
	go func() {
		defer close(tasks)
		scanErr <- scanner.Scan(*src, *dst, includeDir, mode, opts, tasks)
	}()

	pool := worker.New(*workers, *verbose, *bandwidth)
	report, err := pool.Run(tasks)
	if err != nil {
		return err
	}

	var pdfPath, csvPath string
	if reportPDF != nil {
		pdfPath = *reportPDF
	}
	if reportCSV != nil {
		csvPath = *reportCSV
	}
	if err := handleSyncReportOutput(report, cfg, pdfPath, csvPath); err != nil {
		return err
	}

	if err := <-scanErr; err != nil {
		return err
	}
	return nil
}

func writeReportFile(path string, writer func(io.Writer) error) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := writer(f); err != nil {
		return err
	}
	return f.Sync()
}

// HasTrailingSeparator reports whether the provided path ends with the platform separator.
func HasTrailingSeparator(path string) bool {
	if path == "" {
		return false
	}
	return strings.HasSuffix(path, string(os.PathSeparator))
}
