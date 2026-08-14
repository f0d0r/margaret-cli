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

### Scanning over FTP

`<path>` can also be an `ftp://` URL. The FTP tree is walked and read over the
network; ebooks and archives are spooled lazily instead of being downloaded to
disk first.

```sh
margaret-tools scan ftp://192.168.178.1/network/backup
```

If the server requires credentials, pass them with `--ftp-user` and
`--ftp-pass`. When neither is given, the anonymous login is used:

```sh
margaret-tools scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret
```

> **Note for FTP scans:** one FTP connection is opened per worker, and each
> connection can transfer one file at a time. Many consumer routers and NAS
> devices (for example the FRITZ!Box that often lives at `192.168.178.1`)
> only accept one or two simultaneous FTP connections and reject any further
> ones, which shows up as connection errors during the scan. If that happens,
> lower the worker count:
>
> ```sh
> margaret-tools scan ftp://192.168.178.1/network/backup --workers 1
> ```
>
> `--workers 1` is the safe choice for such devices, but it is a
> recommendation, not a hard limit — if your server accepts multiple
> connections, keep the default worker count.

### Supported file formats

Ebooks (metadata is read from these):

- `.epub`
- `.mobi`
- `.azw3`
- `.azw`
- `.prc`

Archives (searched for ebooks inside):

- `.zip`
- `.tar`
- `.tgz` / `.tar.gz`
- `.gz`
- `.bz2`
- `.tbz2` / `.tar.bz2`
- `.rar`
- `.7z`

Ebooks nested inside archives are handled in place — they are read from the
archive without being extracted to disk. Archives may be nested inside other
archives, up to the depth given by `--archive-depth`.

---

## Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--workers` | `int` | number of CPUs | Number of worker goroutines used for scanning and parsing. |
| `--archive-depth` | `int` | `2` | Maximum archive nesting depth to unpack. |
| `--archive-password` | `string` | `""` | Password for encrypted rar and 7z archives. |
| `--spool-mem-limit` | `int` | `67108864` | Max bytes per archive member buffered in memory before spilling to a temp file. |
| `--ftp-user` | `string` | `anonymous` | FTP username for `ftp://` scans. |
| `--ftp-pass` | `string` | `anonymous` | FTP password for `ftp://` scans. |
| `--failures-out` | `string` | `failures.json` | Path to write a JSON report of the failed items. |
| `-h`, `--help` | | | Show help. |

### `--workers`

Sets the number of concurrent workers for both the directory scan and the
metadata parsing pipeline. The queue size is derived from this value
(`workers * 2`). When omitted, the number of CPUs is used.

For FTP scans one connection is opened per worker (see
[Scanning over FTP](#scanning-over-ftp)). If the server rejects concurrent
connections, lower this to `1`.

### `--archive-depth`

Controls how many levels of nested archives are unpacked. A depth of `0` means
the archive itself is opened but nothing inside is processed; an ebook directly
inside an archive is at depth `1`. If an archive is nested deeper than this
limit, the scan records it as a failure.

### `--archive-password`

Supplies the password for encrypted **rar** and **7z** archives. Both formats
are supported: members are decrypted in memory while being read. AES-encrypted
**zip** members are not supported yet; they are reported as failures.

### `--spool-mem-limit`

Controls how much of each ebook or archive found inside an archive is buffered
in memory before the rest is spilled to a temporary file (see
[Memory usage](#memory-usage)). The value is the limit **per member** in bytes;
a value of `0` restores the default of 64 MiB. Only members that exceed the
limit create a temp file, and those files are deleted as soon as the member is
done processing.

### `--failures-out`

The path where the JSON report of failed items is written. Defaults to
`failures.json` in the current directory.

### `--ftp-user` / `--ftp-pass`

Credentials for `ftp://` scans. Both default to the anonymous login
(`anonymous` / `anonymous@`). They are optional: an FTP URL may embed
credentials directly (`ftp://user:pass@host/path`), but using the flags keeps
the password out of the command line.

---

## Memory usage

Ebooks found inside archives are read in place — they are never extracted to
disk. Instead, each archive member is spooled lazily: the part of the file the
metadata reader actually needs (for most formats only a small prefix) is read
straight from the archive. If the metadata lives at the end of the file (as
for `.epub`, whose central directory sits at the end), the whole member has to
be pulled from the archive, but it is kept **in memory** up to
`--spool-mem-limit` (default 64 MiB) per member; anything beyond that is
spilled to a temporary file that is removed when the member is done.

The memory limit is per member, and several members are processed concurrently
(one per `--workers`), so peak memory can be up to
`workers × spool-mem-limit`. Lower the limit if you scan with many workers or
have little RAM; raise it to avoid temp files on a slow disk:

```sh
# keep at most 16 MiB of each member in memory
margaret-tools scan ./books --spool-mem-limit 16777216

# keep at most 256 MiB in memory (avoids temp files for large ebooks)
margaret-tools scan ./books --spool-mem-limit 268435456
```

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

Unpack password-protected rar and 7z archives:

```sh
margaret-tools scan ./books --archive-password letmein
```

Scan with a small in-memory spool limit, e.g. 32 MiB per member:

```sh
margaret-tools scan ./books --spool-mem-limit 33554432
```

Scan a bookshelf on an FTP server with the default (anonymous) login:

```sh
margaret-tools scan ftp://192.168.178.1/network/backup
```

Scan over FTP with a username and password:

```sh
margaret-tools scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret
```

Scan over FTP with a single worker, for routers and NAS devices that only
accept one concurrent FTP connection:

```sh
margaret-tools scan ftp://192.168.178.1/network/backup --workers 1
```

Scan over FTP with credentials and a single worker:

```sh
margaret-tools scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret --workers 1
```