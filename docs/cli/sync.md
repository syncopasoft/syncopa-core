# `syncopa-core sync`

Execute the tasks required to align a destination tree with a source tree. The
sync command consumes the same reconciliation logic as [`scan`](scan.md) but
actually performs filesystem changes.

## Synopsis

```bash
syncopa-core sync --src <path> --dst <path> [--mode update|mirror|sync] [options]
```

## Options

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `--src` | string | _(required)_ | Source directory. |
| `--dst` | string | _(required)_ | Destination directory. |
| `--mode` | string | `update` | Reconciliation mode. See [scan](scan.md#modes). |
| `--workers` | int | `4` | Number of concurrent worker goroutines used to process tasks. |
| `--bandwidth` | int64 (bytes/sec) | `0` | Throttle copy throughput. Zero disables throttling. |
| `--batch-threshold` | int64 (bytes) | `0` | Enable batching for files smaller than or equal to the threshold. |
| `--batch-max-files` | int | `0` | Maximum number of files per batch archive. |
| `--batch-max-bytes` | int64 (bytes) | `0` | Maximum total bytes per batch archive. |
| `--auto-batch` | bool | _(varies)_ | Optional knob for automatically determining batching parameters. |
| `--report-pdf` | string | `` | Write a PDF summary report (when compiled with enterprise reporting). |
| `--report-csv` | string | `` | Write a CSV detail report (when compiled with enterprise reporting). |
| `--verbose` | bool | `false` | Log additional context during execution. |

## Exit codes

* `0` – Sync completed successfully.
* `1` – Failure preparing tasks or performing filesystem operations.

## Behaviour

1. The command triggers a scan in the background and feeds tasks into a worker
   pool.
2. Workers process tasks concurrently, copying files or deleting destinations as
   required. Batches are unpacked using streaming tar archives to keep memory
   usage predictable.
3. A final run report is printed to standard output when summary printing is
   enabled.

## Examples

Copy everything from `/data/raw` to `/data/processed` using eight workers and a
100 MiB/s throttle:

```bash
syncopa-core sync --src /data/raw --dst /data/processed \
  --workers 8 --bandwidth $((100 * 1024 * 1024))
```

Mirror a directory and produce a CSV report:

```bash
syncopa-core sync --src ./input --dst ./mirror \
  --mode mirror --report-csv reports/mirror.csv
```

## Troubleshooting

* **`src and dst required` error:** ensure both flags are specified. Relative
  paths are accepted but must exist.
* **Slow throughput:** increase `--workers`, raise `--bandwidth`, or enable
  batching options if you have many small files.
* **Permission denied:** run the command with sufficient privileges or adjust
  directory ownership before syncing.

## Related topics

* [Scan command reference](scan.md)
* [Man page](../man/syncopa-core.1)
