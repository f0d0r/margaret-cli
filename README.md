# margaret-cli

A command-line tool that scans a directory tree for ebook files, reads their
metadata (author and title), and reports the results. It searches for ebooks
both directly on disk and inside archives, following nested archives up to a
configurable depth.

---

## Building

Requires Go 1.26+.

```sh
make build        # builds ./build/margaret
make test         # runs the test suite
make vet          # runs go vet
make run ARGS="scan <path>"
```

Or without the Makefile:

```sh
go build -o build/margaret ./cmd/margaret-cli
```

---

## Usage

The tool has three commands: `scan` walks a directory recursively and
processes every ebook it finds, `report` regenerates the JSON reports from
an existing database, and `search` finds books and authors in the database
without scanning.

```
margaret scan <path> [flags]
margaret search <query> [flags]
```

`<path>` must be a directory. The scan is recursive: ebooks in subdirectories
are found too.

### Scanning over FTP

`<path>` can also be an `ftp://` URL. The FTP tree is walked and read over the
network; ebooks and archives are spooled lazily instead of being downloaded to
disk first.

```sh
margaret scan ftp://192.168.178.1/network/backup
```

If the server requires credentials, pass them with `--ftp-user` and
`--ftp-pass`. When neither is given, the anonymous login is used:

```sh
margaret scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret
```

> **Note for FTP scans:** one FTP connection is opened per worker, and each
> connection can transfer one file at a time. Many consumer routers and NAS
> devices (for example the FRITZ!Box that often lives at `192.168.178.1`)
> only accept one or two simultaneous FTP connections and reject any further
> ones, which shows up as connection errors during the scan. If that happens,
> lower the worker count:
>
> ```sh
> margaret scan ftp://192.168.178.1/network/backup --workers 1
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
| `--duplicates-out` | `string` | `duplicates.json` | Path to write a JSON report of the duplicate books. |
| `--books-out` | `string` | `books.json` | Path to write a JSON report of the grouped books. |
| `--report` | `bool` | `false` | Write the books and duplicates JSON reports (failures are always written). |
| `--db` | `string` | `margaret.db` | SQLite database file to use. |
| `--json` | `bool` | `false` | Write search results in JSON format (search only). |
| `--json-out` | `string` | `search.json` | Path to write the JSON search results (search only). |
| `--author` | `bool` | `false` | Search authors instead of both titles and authors (search only). |
| `--title` | `bool` | `false` | Search titles instead of both titles and authors (search only). |
| `--limit` | `int` | `0` | Limit the number of search results (`0` = unlimited, search only). |
| `--fresh` | `bool` | `false` | Delete existing scan data and start from a clean slate. |
| `--resume` | `bool` | `false` | Keep existing scan data and only process never-seen paths. |
| `--ext-stats` | `bool` | `false` | Print per-extension file counts (supported vs unsupported) after the scan. |
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

### `--duplicates-out`

The path where the JSON report of duplicate books is written. Defaults to
`duplicates.json` in the current directory. Each entry lists the original book
(`authors`, `title`, `path`) and every other path that holds the same content
(`duplicates`). When no duplicates are found the file is not created. Only
written when `--report` is set.

### `--books-out`

The path where the JSON report of the grouped books is written. Defaults to
`books.json` in the current directory. See
[How books are grouped and described](#how-books-are-grouped-and-described).
When no books were found the file is not created. Only written when
`--report` is set.

### `--report`

Writes the two bulk JSON reports — the grouped books (`--books-out`) and the
duplicate books (`--duplicates-out`). Off by default: at hundreds of thousands
of books these files grow to hundreds of megabytes, while the database itself
is already kept as a stable, queryable artifact (see [`--db`](#--db)), so
there is no reason to pay that disk cost on every run.

`failures.json` is not affected: it is the error channel (like logs), normally
small, and is written on every run, as is the stdout summary.

```sh
margaret scan ./books --report
```

### `--ftp-user` / `--ftp-pass`

Credentials for `ftp://` scans. Both default to the anonymous login
(`anonymous` / `anonymous@`). They are optional: an FTP URL may embed
credentials directly (`ftp://user:pass@host/path`), but using the flags keeps
the password out of the command line.

### `--db`

The SQLite database file used during the scan. Defaults to `margaret.db` in
the current directory. Pending schema migrations are applied automatically
with goose, so an existing database file is reused and only migrated forward.

A scan with `--fresh` (or over an empty database) starts with a clean slate:
previous scan data is deleted first (the schema and migration history are
kept), so re-running a scan never mixes results from earlier runs. With
`--resume` existing rows are kept instead (see
[`--fresh` / `--resume`](#fresh---resume)). Pass a custom path to use a
different database file, or `":memory:"` for an ephemeral database that is
discarded when the scan finishes:

```sh
margaret scan ./books --db /tmp/test.db
margaret scan ./books --db ":memory:"
```

If the parent directory of a custom path does not exist, the scan fails with
an error. Do not run two scans against the same database file in parallel.

### `--fresh` / `--resume`

Controls what happens when the database already holds scanned data:

- `--fresh` deletes the existing data first (same as the default behavior on
  an empty database) and rescans everything.
- `--resume` keeps the existing rows: files whose path is already recorded
  are skipped without being read again — even if they changed on disk —
  while never-seen paths are processed. Failures leave no row behind, so
  they are retried on every run. The scan summary reports skipped files
  separately.

With neither flag and a non-empty database, an interactive terminal asks
whether to resume (the default) or start fresh, showing the database path,
the recorded book/file counts and the previous scan's root and time. Without
a terminal (scripts, CI, cron) the scan fails instead with an error telling
you to pass `--fresh` or `--resume` — so automation can never silently wipe
or mix data. The two flags are mutually exclusive.

Resuming a *different* tree into the same database warns explicitly on a
terminal and is refused without one: mixing two trees is almost never
intended, so use `--fresh` or a different `--db` file instead.

### Crash and re-run behavior

Every successfully parsed file is committed to the database immediately (one
transaction per file), so interrupting a scan with `Ctrl-C` keeps everything
processed so far — only the in-progress files are lost. Re-running with
`--resume` continues where the interrupted run stopped: finished files are
skipped, failed files (which leave no row behind) are retried. Each run is
recorded with its root, mode and outcome; the re-run prompt reads the latest
entry.

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
margaret scan ./books --spool-mem-limit 16777216

# keep at most 256 MiB in memory (avoids temp files for large ebooks)
margaret scan ./books --spool-mem-limit 268435456
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

- **Total** — total number of ebook files encountered (succeeded + failed + skipped).
- **Succeeded** — ebooks whose metadata was read successfully.
- **Failed** — items that could not be processed.
- **Skipped** — recorded files passed over without reading (resume runs only).
- **Duration** — wall-clock time of the scan.

With `--ext-stats` the summary is followed by per-extension file counts,
split into supported and unsupported. Supported archives are treated as
folders and excluded; files on disk and inside archives are both counted.
The percentages sum to 100% over supported + unsupported:

```
Extension stats (10 files):
Supported:
  epub: 3 (30%)
  mobi: 2 (20%)
Unsupported:
  pdf: 4 (40%)
  txt: 1 (10%)
```

```sh
margaret scan ./books --ext-stats
```

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

Duplicate books are reported in `duplicates.json`. Each entry describes the
original book and every other path that holds the same content:

```json
[
  {
    "authors": "Herman Melville",
    "title": "Moby Dick",
    "path": "books/mobydick.epub",
    "duplicates": ["backup/mobydick.epub", "mirror/mobydick.epub"]
  }
]
```

When no duplicates are found the file is not created.

Grouped books are reported in `books.json` (see
[How books are grouped and described](#how-books-are-grouped-and-described)).
Each entry holds the chosen book-level metadata plus every grouped file with
its own metadata:

```json
[
  {
    "authors": ["Herman Melville"],
    "title": "Moby Dick",
    "files": [
      {
        "path": "books/mobydick.epub",
        "authors": ["Herman Melville"],
        "title": "Moby Dick"
      },
      {
        "path": "backup/mobydick.mobi",
        "authors": ["Herman Melville"],
        "title": "Moby Dick; Or, The Whale"
      }
    ]
  }
]
```

When no books were found the file is not created.

---

## How books are grouped and described

### Exact duplicates

Files with byte-identical content (same content hash) are exact duplicates.
The first path seen wins; the rest are listed under it in `duplicates.json`.
Exact duplicates never get a book of their own.

### Near-duplicate grouping

Every other file carries a 128-number MinHash content fingerprint. During
the scan each new file is grouped as follows:

1. Its MinHash signature is split into 25 bands of 5 numbers (LSH banding).
   Files sharing at least one band hash become candidates.
2. Candidates are verified with exact Jaccard similarity in Go.
3. At Jaccard ≥ 75% the file joins the best-matching candidate's book
   (greedy best match, no transitive merging of books); otherwise a new
   book is created.

Every file belongs to exactly one book. Grouping runs inside the scan
transaction, so results do not depend on worker scheduling.

### Book-level metadata

`books.json` picks one title and one author list per book; the per-file
entries keep their original titles (whitespace-normalized) and their
placeholder-filtered authors. All of the below runs at report time only —
the database is never rewritten:

- **Normalization.** Comparisons use trimmed, whitespace-collapsed,
  lower-cased keys; diacritics are folded (NFD mark-stripping for Latin
  script only, so Cyrillic `й` and Indic scripts survive, plus `ø→o`,
  `ß→ss`, `æ→ae`, `œ→oe`, `ł→l`, `đ→d`, `þ→th`, `ı→i`).
- **Author placeholders are dropped.** Converter defaults such as
  `Unknown`, `Ismeretlen`, `Inconnu`, `Unbekannt`, `Desconocido`,
  `Sconosciuto`, `Onbekend`, `Nieznany`, `Okänd`, `Bilinmiyor`,
  `Неизвестный`, `مجهول`, `نامعلوم`, `अज्ञात`, `未知`, `佚名`, `不明`,
  `미상`, `Névtelen`, `User`, `N/A` (full list in code). An author name
  identical to its file's title is also dropped (converters sometimes copy
  the title into the author field).
- **Author vote.** The most common full author list wins; `Last, First`
  spellings vote together with `First Last` ones (display keeps the
  first-seen spelling and order). Ties break towards more authors, then
  more characters, then more space-containing names, then first-seen.
- **Title placeholders are dropped.** `Untitled`, `Sans titre`,
  `Ohne Titel`, `Sin título`, `Senza titolo`, `Sem título`,
  `Zonder titel`, `Bez tytułu`, `Utan titel`, `Başlıksız`, `Без названия`,
  `بدون عنوان`, `无标题`, `無題`, `제목 없음`, `Névtelen` (full list in
  code).
- **Title vote.** The most common title wins. Ties break towards fewer
  underscores (machine-stamped titles use them, clean ones use spaces),
  then towards titles not mentioning a known author, then longer, then
  first-seen.
- **Author-in-title penalty (tie-break only).** A title containing every
  distinctive token of a known author (`Steven Saylor - X`) loses ties.
  Tokens of 1–2 letters and particles (`van`, `von`, `del`, …) do not
  count; single-token names can still match on their one word.
- **Filename fallback.** With no usable title at all, the first file's
  name is used (archive prefixes and directories dropped, known ebook
  extension stripped, underscores to spaces). File entries keep their
  empty titles.
- **Calibre `Title - Author` filenames.** With no usable authors, member
  filenames are scanned in path order: the strict form needs a comma
  (`X - Last, First`) and can also supply the title; next to a known-good
  title a comma-less two-word author (`X - First Last`) is accepted for
  the authors only. Authors recovered this way are stored verbatim.
- **Credit-affix wash.** With known authors, a leading `Author - ` or
  trailing ` - Author` / ` by Author` is stripped from the picked title
  (whole-word, order-free, diacritic-folded match; the result must stay
  non-empty). Genuine titles such as `Stephen King Goes to the Movies`
  are unaffected.

The rules above naturally cannot guarantee fully correct data: metadata
incorrectness occurs in a huge variety of forms, so the reports are worth
checking by hand.

---

## Commands

Root-level commands:

- `scan` — scan a directory for ebook files.
- `report` — regenerate JSON reports from the database.
- `search` — search books and authors in the database (see
  [Searching](#searching)).
- `help` — show help for any command.
- `completion` — generate shell autocompletion scripts.

Run `margaret --help` or `margaret scan --help` for details.

## Regenerating reports

`scan` writes the `books.json` / `duplicates.json` reports only with
`--report`, but the database always holds the full data. `report` regenerates
both files from an existing database without scanning — after a report-less
scan, when the JSON files were deleted, or when they are wanted at different
paths:

```sh
margaret report
margaret report --db /tmp/test.db --books-out /tmp/books.json
```

`failures.json` cannot be regenerated: failures are produced during the scan
and are not stored in the database. Against an empty or missing database the
command fails with an error pointing to `scan`, instead of writing empty
reports or creating an empty database file.

---

## Searching

`search` answers "how do I find a book or an author in this collection?"
from the database, without running a scan. Both titles and authors are
matched with a full-text index (trigram, diacritic-insensitive), so partial
words and accented names match:

```sh
margaret search "Asimov"
margaret search --author "King"
margaret search --title "Foundation" --limit 5
```

By default both titles and authors are searched and hits are ordered by
relevance. `--author` restricts the search to authors, `--title` to titles
(both flags together mean the default). `--limit` caps the number of hits;
`0` (the default) means unlimited. Only letters and digits count towards
the query: punctuation and FTS5 syntax characters (`"`, `*`, parentheses,
`:`) are ignored and bare `AND`/`OR`/`NOT` words are dropped, so they can
never break the search; an empty query is an error.

Two matching rules follow from the trigram index: every word must match
within a single field — all words have to occur in the title or all in one
author name, so `Asimov Foundation` finds nothing unless one field holds
both words — and words shorter than 3 characters never match, so e.g. `Ed`
alone finds nothing.

The human-readable table (authors, title, file paths) goes to **stdout**.
With `--json` the hits are written in the `books.json` shape (same entries,
restricted to the hits in relevance order) to `--json-out` (default
`search.json` in the current directory):

```sh
margaret search "Asimov" --json --json-out /tmp/hits.json
```

With no hits nothing is printed and no file is created. Like `report`,
`search` is read-only: against an empty or missing database it fails with an
error naming the database file and pointing to `scan`, and it never creates
a missing database file.

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
margaret scan ./books
```

Scan using 8 workers and allow deep archive nesting:

```sh
margaret scan ./books --workers 8 --archive-depth 5
```

Write the failures report to a custom location:

```sh
margaret scan ./books --failures-out /tmp/failures.json
```

Scan into a custom database file instead of the default `./margaret.db`:

```sh
margaret scan ./books --db /tmp/test.db
```

Unpack password-protected rar and 7z archives:

```sh
margaret scan ./books --archive-password letmein
```

Scan with a small in-memory spool limit, e.g. 32 MiB per member:

```sh
margaret scan ./books --spool-mem-limit 33554432
```

Scan a bookshelf on an FTP server with the default (anonymous) login:

```sh
margaret scan ftp://192.168.178.1/network/backup
```

Scan over FTP with a username and password:

```sh
margaret scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret
```

Scan over FTP with a single worker, for routers and NAS devices that only
accept one concurrent FTP connection:

```sh
margaret scan ftp://192.168.178.1/network/backup --workers 1
```

Scan over FTP with credentials and a single worker:

```sh
margaret scan ftp://192.168.178.1/network/backup --ftp-user backup --ftp-pass secret --workers 1
```

Search books and authors from a previous scan:

```sh
margaret search "Asimov"
```

Search only authors, at most 5 hits:

```sh
margaret search --author "King" --limit 5
```

Write the hits in the `books.json` shape:

```sh
margaret search "Foundation" --json --json-out /tmp/hits.json
```