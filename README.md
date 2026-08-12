# margaret-tools

A command-line tool that scans a directory tree for ebook files, reads their
metadata (author and title), and reports the results. It searches for ebooks
both directly on disk and inside archives, following nested archives up to a
configurable depth.

---

## Building

Requires Go 1.26+.

```sh
make build        # builds ./build/margaret-tools
make test         # runs the test suite
make vet          # runs go vet
make run ARGS="scan <path>"
```

Or without the Makefile:

```sh
go build -o build/margaret-tools ./cmd/margaret-tools
```

---

## Usage

The tool has a single command, `scan`, which walks a directory recursively and
processes every ebook it finds.

```
margaret-tools scan <path> [flags]
```

`<path>` must be a directory. The scan is recursive: ebooks in subdirectories
are found too.

### Supported file formats

Ebooks (metadata is read from these):

- `.epub`
- `.mobi`
- `.azw3`
- `.azw`
- `.prc`
- `.pdf`

Archives (searched for ebooks inside):

- `.zip`
- `.tar`
- `.tgz` / `.tar.gz`
- `.gz`
- `.bz2`
- `.tbz2` / `.tar.bz2`

Ebooks nested inside archives are handled in place — they are read from the
archive without being extracted to disk. Archives may be nested inside other
archives, up to the depth given by `--archive-depth`.

---

## Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--workers` | `int` | number of CPUs | Number of worker goroutines used for scanning and parsing. |
| `--archive-depth` | `int` | `2` | Maximum archive nesting depth to unpack. |
| `--archive-password` | `string` | `""` | Password for encrypted archives. **Reserved, not yet supported.** |
| `--failures-out` | `string` | `failures.json` | Path to write a JSON report of the failed items. |
| `-h`, `--help` | | | Show help. |

### `--workers`

Sets the number of concurrent workers for both the directory scan and the
metadata parsing pipeline. The queue size is derived from this value
(`workers * 2`). When omitted, the number of CPUs is used.

### `--archive-depth`

Controls how many levels of nested archives are unpacked. A depth of `0` means
the archive itself is opened but nothing inside is processed; an ebook directly
inside an archive is at depth `1`. If an archive is nested deeper than this
limit, the scan records it as a failure.

### `--archive-password`

Reserved for future encrypted-archive support. It is currently **not**
implemented: the standard library cannot decrypt RAR or AES-encrypted zip
files. Password-protected zip members are reported as failures.

### `--failures-out`

The path where the JSON report of failed items is written. Defaults to
`failures.json` in the current directory.

---

## Output

During a run, a single-line progress bar is drawn to **stderr** (it shows a
spinner, a progress bar, a counter, and the file currently being processed).
It is rewritten in place and never wraps, so your terminal stays clean.

When the scan finishes, the final report is printed to **stdout**:

```
Total      2 ebook(s)
Succeeded  2
Failed     0
Duration   1ms
```

- **Total** — total number of ebook files found (succeeded + failed).
- **Succeeded** — ebooks whose metadata was read successfully.
- **Failed** — items that could not be processed.
- **Duration** — wall-clock time of the scan.

The report is followed by the failures JSON file. Empty output is written even
when nothing failed:

```json
[]
```

When items fail, the report lists each item and the reason:

```json
[
  {
    "path": "books/secret.zip!book.epub",
    "error": "password-protected zip member; --archive-password is not supported yet"
  }
]
```

Note that paths of items found inside archives are shown as
`<archive-path>!<member-name>`.

---

## Commands

Root-level commands:

- `scan` — scan a directory for ebook files.
- `help` — show help for any command.
- `completion` — generate shell autocompletion scripts.

Run `margaret-tools --help` or `margaret-tools scan --help` for details.

---

## Exit codes and cancellation

- The command exits non-zero if the root path is inaccessible or not a
  directory, or if the failures report cannot be written.
- The scan can be interrupted with `Ctrl-C`; it cancels gracefully and writes
  the failures report before exiting.

---

## Examples

Scan a bookshelf with the default settings:

```sh
margaret-tools scan ./books
```

Scan using 8 workers and allow deep archive nesting:

```sh
margaret-tools scan ./books --workers 8 --archive-depth 5
```

Write the failures report to a custom location:

```sh
margaret-tools scan ./books --failures-out /tmp/failures.json
```