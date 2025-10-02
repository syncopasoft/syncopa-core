package main

import (
	"fmt"
	"log"
	"os"

	"syncopa/internal/cli"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "scan":
		autoBatch := false
		if err := cli.RunScan(os.Args[2:], cli.ScanConfig{
			AutoBatch: cli.AutoBatchConfig{EnableFlag: false, Default: false, Forced: &autoBatch},
		}); err != nil {
			log.Fatal(err)
		}
	case "sync":
		autoBatch := false
		if err := cli.RunSync(os.Args[2:], cli.SyncConfig{
			AutoBatch:          cli.AutoBatchConfig{EnableFlag: false, Default: false, Forced: &autoBatch},
			PrintSummary:       true,
			PrintVerboseReport: false,
		}); err != nil {
			log.Fatal(err)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Println("expected 'scan' or 'sync' subcommands")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("Usage: %s <command> [options]\n", os.Args[0])
	fmt.Println("Commands:")
	fmt.Println("  scan   Plan migration tasks")
	fmt.Println("  sync   Execute migration tasks")
	fmt.Printf("Use '%s <command> --help' for command-specific options.\n", os.Args[0])
}
