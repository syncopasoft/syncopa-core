# Syncopa Core

Syncopa Core is the open-source foundation for Syncopa's high performance data
movement tooling. It provides:

* A reusable scanning engine that can analyse file trees, detect differences,
  and emit high level copy/delete tasks.
* A worker pool capable of executing individual copy jobs or compact tar-based
  batches with progress reporting hooks.
* Simple command line interfaces that expose the scanning and syncing logic for
  automation or experimentation.

The enterprise product consumes these packages directly, but they are designed
to stand alone so you can embed them in your own workflows as well.

---

## Table of contents

1. [Getting started](#getting-started)
2. [Command line usage](#command-line-usage)
3. [Library integration](#library-integration)
4. [Development](#development)
5. [Additional documentation](#additional-documentation)
6. [License](#license)

## Getting started

### Requirements

* Go 1.21 or newer
* A POSIX-like environment for running the CLI examples (Linux, macOS, WSL)

### Install the CLI

`syncopa-core` ships a reference command line interface that can be installed
directly with `go install`. The main package lives under the `cmd` directory, so
be sure to include that path when installing:

```bash
go install github.com/syncopasoft/syncopa-core/cmd/syncopa-core@latest
```

This places the binary in your Go bin directory (typically `$(go env GOPATH)/bin`
or `$GOBIN`). Make sure that directory is on your `$PATH` so you can run the
tool directly.

If you prefer to build from source after cloning the repository, make sure you
are inside the repository directory and then run:

```bash
cd syncopa-core
go build ./cmd/syncopa-core
```

The resulting binary exposes the `scan` and `sync` subcommands described below.

## Command line usage

The `syncopa-core` binary offers a high level interface for planning and
executing file migrations. Use `syncopa-core help` or `syncopa-core <command>
--help` to print inline help.

### Scan

Generate a migration plan by comparing a source and destination tree:

```bash
syncopa-core scan --src /data/source --dst /data/target \
  --mode mirror --verbose
```

Key flags:

| Flag | Description |
| ---- | ----------- |
| `--mode` | `update` (default), `mirror`, or `sync` reconciliation strategy. |
| `--batch-threshold` | Maximum size (bytes) for automatic batching. |
| `--batch-max-files` / `--batch-max-bytes` | Explicit batch limits when batching is enabled. |
| `--verbose` | Print additional context such as the detected mode for each task. |

See [docs/cli/scan.md](docs/cli/scan.md) for an in-depth walk-through of the
output format, batching heuristics, and troubleshooting tips.

### Sync

Execute a migration plan and optionally export reports:

```bash
syncopa-core sync --src /data/source --dst /data/target \
  --workers 8 --bandwidth 125000000
```

Key flags:

| Flag | Description |
| ---- | ----------- |
| `--workers` | Number of concurrent workers used for copy operations. |
| `--bandwidth` | Throttle copy throughput (bytes/sec, `0` for unlimited). |
| `--mode` | Reconciliation strategy identical to `scan`. |
| `--report-pdf` / `--report-csv` | Persist run summaries when enabled. |

The sync workflow, reporting hooks, and error handling are covered in
[docs/cli/sync.md](docs/cli/sync.md).

### Sample data generator

The [`sampletool`](cmd/sampletool) binary can create realistic directory trees
for testing strategies. Run `go run ./cmd/sampletool --help` to explore the
available knobs.

## Library integration

Syncopa Core exposes packages that can be embedded in other applications. A
minimal scanning example looks like this:

```go
package main

import (
    "fmt"

    "github.com/syncopasoft/syncopa-core/internal/scanner"
    "github.com/syncopasoft/syncopa-core/internal/task"
)

func main() {
    tasks := make(chan task.Task)
    go func() {
        defer close(tasks)
        err := scanner.Scan("/tmp/src", "/tmp/dst", false, scanner.ModeUpdate, scanner.Options{}, tasks)
        if err != nil {
            panic(err)
        }
    }()

    for t := range tasks {
        fmt.Printf("%s -> %s (%v)\n", t.Src, t.Dst, t.Action)
    }
}
```

Explore the `internal` packages for additional entry points:

* `internal/scanner` – snapshotting, task generation, and batching heuristics.
* `internal/worker` – worker pools, copy execution, report aggregation.
* `internal/cli` – helper wiring for building custom CLIs around the core.

## Development

1. Ensure Go 1.21+ is installed (`go version`).
2. Run the tests with:

   ```bash
   go test ./...
   ```

3. Format Go code with `gofmt -w` before submitting patches.

Continuous integration guidelines and extension points are documented in
[docs/development.md](docs/development.md).

## Additional documentation

* [CLI reference for scan](docs/cli/scan.md)
* [CLI reference for sync](docs/cli/sync.md)
* [Contributor and release workflow](docs/development.md)
* Manual pages under `docs/man/` (`man -l docs/man/syncopa-core.1`)

## License

Syncopa Core is licensed under the Apache License, Version 2.0. Refer to the
[LICENSE](LICENSE) and [NOTICE](NOTICE) files for details.
