# Development guide

This document outlines common workflows for contributors extending Syncopa Core.

## Prerequisites

* Go 1.21 or newer (`go version`)
* GNU Make (optional but helpful for scripting tasks)
* Access to POSIX-like tooling (`tar`, `bash`, `sed`, ...)

## Repository layout

```
cmd/              Reference binaries (`syncopa-core`, `sampletool`)
internal/         Reusable packages consumed by the binaries and external apps
  cli/            Wiring code for CLI front-ends
  config/         Configuration parsing helpers and tests
  scanner/        Filesystem diffing and task generation logic
  task/           Shared task definitions
  worker/         Execution engine and reporting helpers
```

## Common tasks

### Run the test suite

```
go test ./...
```

Tests are designed to run quickly and rely only on temporary directories. File
system access is limited to the test-specific temp dirs, so the suite is safe to
run on development machines.

### Format and lint

```
gofmt -w ./
```

The project intentionally stays close to the standard library, so additional
linters are optional but welcome (`golangci-lint`, `staticcheck`, etc.).

### Updating dependencies

The module has no external runtime dependencies at the moment. If you add new
imports, run `go mod tidy` to update `go.mod` and `go.sum`.

### Building the CLI

```
go build ./cmd/syncopa-core
```

Place the resulting binary on your `PATH` or run it with `go run` while
iterating.

### Generating sample data

```
go run ./cmd/sampletool --dir ./testdata --count 50 --sizes 64KB,1MB --max-bytes 100MB
```

This produces a realistic mix of office documents and nested directories for
benchmarking scan/sync runs.

## Release process

1. Ensure tests pass locally (`go test ./...`).
2. Update the changelog or release notes as appropriate.
3. Tag the release using [Semantic Versioning](https://semver.org/).
4. Publish the tag and binaries to your distribution channel of choice.

## Support

File issues or feature requests in the repository issue tracker. For commercial
support inquiries contact the Syncopa team at `support@syncopa.example`.
