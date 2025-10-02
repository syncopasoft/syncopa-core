# `syncopa-core scan`

Plan the actions required to align a destination file tree with a source tree.
The scan command analyses both hierarchies, calculates the differences, and
streams a sequence of copy/delete tasks. It does not modify the filesystem.

## Synopsis

```bash
syncopa-core scan --src <path> --dst <path> [--mode update|mirror|sync] [options]
```

## Modes

| Mode | Description |
| ---- | ----------- |
| `update` | Copy new or modified files from the source to the destination. Extra destination files are left untouched. |
| `mirror` | Copy new or modified files and delete any destination files that are not present on the source. |
| `sync` | Keep both locations aligned by copying newer files in either direction. |

## Options

| Flag | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `--src` | string | _(required)_ | Source directory to analyse. |
| `--dst` | string | _(required)_ | Destination directory to analyse. |
| `--mode` | string | `update` | Reconciliation mode described above. |
| `--batch-threshold` | int64 (bytes) | `0` | Maximum file size eligible for batching. When zero batching is disabled. |
| `--batch-max-files` | int | `0` | Maximum number of files per batch. Applies when batching is enabled. |
| `--batch-max-bytes` | int64 (bytes) | `0` | Maximum total bytes per batch archive. Applies when batching is enabled. |
| `--verbose` | bool | `false` | Emit extra context for each task such as which mode produced it. |
| `--auto-batch` | bool | _(varies)_ | When exposed by the embedding application, toggles automatic tuning of batching heuristics. |

> **Note**
> The reference CLI currently disables the `--auto-batch` flag by default. The
> configuration exists so downstream applications can enable it without changing
> the CLI layer.

## Output

Each planned operation is written to standard output. Examples:

```
/data/source/report.pdf -> /data/target/report.pdf
batch 42 files -> /data/target/logs/
delete /data/target/tmp/obsolete.tmp
```

When `--verbose` is set the entries are prefixed with additional context such as
`[copy:mirror]` or `[delete:sync]`.

## Exit codes

* `0` – Scan completed successfully.
* `1` – Invalid flags, missing directories, or an internal failure occurred.

## Examples

Plan a mirror migration while enabling batching for files up to 256 KiB:

```bash
syncopa-core scan --src ~/datasets/source --dst /mnt/storage/archive \
  --mode mirror --batch-threshold $((256 * 1024)) --batch-max-bytes $((4 * 1024 * 1024))
```

Perform a dry-run sync between two USB drives while inspecting verbose output:

```bash
syncopa-core scan --src /Volumes/Camera --dst /Volumes/Backup --mode sync --verbose
```

## Troubleshooting

* **No output appears:** ensure the source directory contains files that differ
  from the destination. Identical trees produce zero tasks.
* **`src and dst required` error:** verify both paths were provided and that you
  used `--src`/`--dst` rather than positional arguments.
* **Unexpected deletes in mirror mode:** confirm the destination path is correct
  and does not point to an already pruned tree. Use `--mode update` to confirm
  which files will be copied before running `mirror`.

## Related topics

* [Sync command reference](sync.md)
* [Man page](../man/syncopa-core.1)
