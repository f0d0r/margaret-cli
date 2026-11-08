# Margaret CLI

[![Build Status](https://github.com/f0d0r/margaret-cli/actions/workflows/build.yml/badge.svg)](https://github.com/f0d0r/margaret-cli/actions/workflows/build.yml)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.26.8-blue)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A command-line tool that scans a directory tree for ebook files, reads their
metadata (author and title), and reports the results. It searches for ebooks
both directly on disk and inside archives, following nested archives up to a
configurable depth.

---

## Building

Requires Go 1.26.8+.

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

```sh
margaret scan ./books
```

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
- `.txt`
- `.text`
- `.rtf`
- `.pdf` (only when Poppler's `pdftotext` and `pdfinfo` are available; see below)

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

Plain-text books are read with a dedicated parser. It detects the encoding
(UTF-8 and UTF-16 BOMs, and legacy code pages from the detector's candidates,
validated by language identification), takes a title and authors from a header
block at the start of the text (`Title:`, `Author:`, `Authors:`, legacy
`TI:`/`AU:` header keys, YAML front matter or a Project Gutenberg preamble) or
from Calibre's title-page layout, and extracts at most one validated ISBN from
the first 64 KiB. The whole decoded text feeds the same 512-hash MinHash
fingerprint as the other formats; the content hash, the decoded head and the
fingerprint come from a single pass over the file. RTF books are decoded to
plain text the same way: the `\info` block supplies the title, authors,
description, subjects and publisher (following Calibre's field mapping), while
documents without `\info` metadata fall back to Calibre's title-page layout.
Files smaller than `--min-doc-size` (15 KiB by default) are treated as
distribution metadata or fragments rather than books and are counted as
skipped. Empty files are counted as skipped as well. Binary files are
reported as failures.

PDF books are read through the system-installed Poppler tools: `pdfinfo`
supplies the Info dictionary and XMP metadata, and `pdftotext` supplies the
plain text that feeds the MinHash fingerprint. Both tools must be on `PATH`
(or in the directory given by `--pdf-tools-dir`). If either is missing, the
scan prints a warning, skips PDF files, and continues with every other
supported format. PDF support is therefore conditional. The same
`--min-doc-size` limit applies to both the raw PDF file and the extracted
text: PDFs whose cleaned text is smaller than the limit are treated as having
no usable text and are skipped. Poppler's `pdftotext` does not perform OCR, so
image-only/scanned PDFs are skipped as well.

Poppler is not bundled. Install it with the operating system package
manager, for example:

```sh
# Debian / Ubuntu
sudo apt install poppler-utils

# Fedora
sudo dnf install poppler-utils

# macOS (Homebrew)
brew install poppler
```

Every ebook's language is verified from its reading-order text with
language-agnostic detection. A missing language is filled in, and a declared
language is replaced only when the detected language is clearly different and
the detector is confident; regional tags that agree with the detection are
kept unchanged.

Metadata and encoding detection are language- and country-independent.
Structural signals (Unicode categories, line shape, file names) come first;
keyword lists are additive hints that no feature requires, so a language
without hints still works.

---

## Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--workers` | `int` | number of CPUs | Number of worker goroutines used for scanning and parsing. |
| `--archive-depth` | `int` | `2` | Maximum archive nesting depth to unpack. |
| `--archive-password` | `string` | `""` | Password for encrypted rar and 7z archives. |
| `--spool-mem-limit` | `int` | `67108864` | Max bytes per archive member buffered in memory before spilling to a temp file. |
| `--min-doc-size` | `int` | `15360` | Minimum size in bytes for a document-format file (txt, rtf, pdf) to be treated as a book; for PDF it also applies to the extracted text (`0` = default, negative = disable). |
| `--pdf-tools-dir` | `string` | `""` | Directory containing Poppler's `pdftotext` and `pdfinfo` (default: search `PATH`). |
| `--ftp-user` | `string` | `anonymous` | FTP username for `ftp://` scans. |
| `--ftp-pass` | `string` | `anonymous` | FTP password for `ftp://` scans. |
| `--failures-out` | `string` | `reports/failures.json` | Path to write a JSON report of the failed items. |
| `--skipped-out` | `string` | `reports/skipped.json` | Path to write a JSON report of the skipped items. |
| `--duplicates-out` | `string` | `reports/duplicates.json` | Path to write a JSON report of the duplicate books. |
| `--books-out` | `string` | `reports/books.json` | Path to write a JSON report of the grouped books. |
| `--report` | `bool` | `false` | Write the books and duplicates JSON reports (failures and skipped reports are always written). |
| `--db` | `string` | `margaret.db` | SQLite database file to use. |
| `--json` | `bool` | `false` | Write search results in JSON format (search only). |
| `--json-out` | `string` | `reports/search.json` | Path to write the JSON search results (search only). |
| `--author` | `bool` | `false` | Search authors instead of both titles and authors (search only). |
| `--title` | `bool` | `false` | Search titles instead of both titles and authors (search only). |
| `--limit` | `int` | `0` | Limit the number of search results (`0` = unlimited, search only). |
| `--fresh` | `bool` | `false` | Delete existing scan data and start from a clean slate. |
| `--resume` | `bool` | `false` | Keep existing scan data and only process never-seen paths. |
| `--ext-stats` | `bool` | `false` | Print per-extension file counts (supported vs unsupported) after the scan. |
| `--cpuprofile` | `string` | `""` | Write a pprof CPU profile of the command to this file. |
| `-h`, `--help` | | | Show help. |
| `-v`, `--version` | | | Print version. |

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

### `--min-doc-size`

Sets the minimum size in bytes for a document-format file (plain text, RTF
and PDF) to be treated as a book. Document collections often contain tiny
side files (`nncl.txt`, `olvasd_el.txt`, `_adatlap.txt`, chapter fragments)
next to the actual books; these are skipped and counted in **Skipped** instead
of becoming books on their own. The default is 15 KiB (15360 bytes) because
side files (readmes, blurbs, file lists) are typically smaller than real
books. A value of `0` uses the default; a negative value disables the size
filter entirely.

For PDFs the same limit is applied twice: the raw PDF must be at least this
large, and the cleaned extracted text must be at least this large. PDFs below
either threshold are treated as empty or fragmentary and skipped.

```sh
# keep only document files of at least 32 KiB
margaret scan ./books --min-doc-size 32768

# disable the size filter
margaret scan ./books --min-doc-size -1
```

Robust content checks still reject HTML sources, HTTP cache dumps, credit
pages, blurbs and tables of contents for plain-text and RTF files.

### `--pdf-tools-dir`

Sets the directory that contains Poppler's `pdftotext` and `pdfinfo`. By
default both are looked up on `PATH`. When either is missing, the scan prints
a warning and skips PDF files; all other supported formats are still
processed. This is useful on systems where Poppler is installed outside the
standard `PATH`.

### `--failures-out`

The path where the JSON report of failed items is written. Defaults to
`reports/failures.json`.

### `--skipped-out`

The path where the JSON report of skipped items is written. Defaults to
`reports/skipped.json`. It lists the files that were passed
over without being parsed because they are not books, are below
`--min-doc-size`, or cannot be processed (for example PDFs while Poppler is
unavailable). Files skipped by a resume run because they were already
processed are **not** listed: there is nothing left to inspect for those.
They are counted separately as already processed.

### `--duplicates-out`

The path where the JSON report of duplicate books is written. Defaults to
`reports/duplicates.json`. Each entry lists the original book
(`authors`, `title`, `path`) and every other path that holds the same content
(`duplicates`). When no duplicates are found the file is not created. Only
written when `--report` is set.

### `--books-out`

The path where the JSON report of the grouped books is written. Defaults to
`reports/books.json`. See
[How books are grouped and described](#how-books-are-grouped-and-described).
When no books were found the file is not created. Only written when
`--report` is set.

### `--report`

Writes the two bulk JSON reports — the grouped books (`--books-out`) and the
duplicate books (`--duplicates-out`). Off by default: at hundreds of thousands
of books these files grow to hundreds of megabytes, while the database itself
is already kept as a stable, queryable artifact (see [`--db`](#--db)), so
there is no reason to pay that disk cost on every run.

`failures.json` and `skipped.json` are not affected: they are the error and
skip channels, normally small, and are written on every run, as is the stdout
summary.

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
  they are retried on every run. The scan summary counts already processed
  files as a separate line, and they are not listed in the skipped report.

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

### `--cpuprofile`

Writes a pprof CPU profile of the whole command (scan, consolidation and
report writing included) to the given file. It works for every subcommand, and
the profile is flushed even when the run is canceled with `Ctrl-C` or fails.
The flag has no effect on the results.

```sh
margaret scan /home/attila/books --db scan.db --cpuprofile cpu.prof
go tool pprof -top margaret cpu.prof
```

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

- **Total** — total number of ebook files encountered
  (`succeeded + failed + skipped + already processed`).
- **Succeeded** — ebooks whose metadata was read successfully.
- **Failed** — items that could not be processed.
- **Skipped** — actionable skips: document files that are too small or
  otherwise not books, empty files, and PDFs skipped because Poppler is
  unavailable or their extracted text is too short. These are listed in
  `skipped.json`.
- **Already processed** — files a resume run passed over because they were
  recorded in an earlier run. These are not listed in `skipped.json`.
- **Duration** — wall-clock time of the scan.

With `--ext-stats` the summary is followed by per-extension file counts,
split into supported and unsupported. Supported archives are treated as
folders and excluded; files on disk and inside archives are both counted.
The percentages sum to 100% over supported + unsupported:

```
Extension stats (10 files):
Supported:
  epub: 3 (30%)
  pdf: 3 (30%)
  mobi: 2 (20%)
  txt: 1 (10%)
Unsupported:
  docx: 1 (10%)
```

```sh
margaret scan ./books --ext-stats
```

The report is followed by the failures and skipped JSON files. Empty output is
written even when nothing failed or was skipped:

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

The skipped report follows the same conventions and is written even when
nothing was skipped:

```json
[]
```

Skipped items list the path and the reason:

```json
[
  {
    "path": "books/short.txt",
    "reason": "below minimum document size (1200 bytes, minimum 15360)"
  },
  {
    "path": "books/bundle.zip!scanned.pdf",
    "reason": "no extractable text"
  }
]
```

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
    "description": "The story of Captain Ahab's hunt for the white whale.",
    "series": "",
    "series_index": "",
    "subjects": ["Sea stories", "Adventure"],
    "languages": ["en"],
    "identifiers": [
      {"scheme": "ISBN", "value": "9780142437247", "source": "METADATA_AND_TEXT"}
    ],
    "publications": [
      {"publisher": "Harper & Brothers", "publication_date": "1851"},
      {"publisher": "Penguin Classics", "publication_date": "2003"}
    ],
    "files": [
      {
        "path": "books/mobydick.epub",
        "authors": ["Herman Melville"],
        "title": "Moby Dick",
        "identifiers": [
          {"scheme": "ISBN", "value": "9780142437247", "source": "METADATA_AND_TEXT"}
        ]
      },
      {
        "path": "backup/mobydick.mobi",
        "authors": ["Herman Melville"],
        "title": "Moby Dick; Or, The Whale",
        "identifiers": [
          {"scheme": "ISBN", "value": "9780142437247", "source": "METADATA_AND_TEXT"}
        ]
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

Every other file carries a 512-number MinHash content fingerprint. During
the scan each new file is grouped as follows:

1. The first 125 values of its MinHash signature are split into 25 bands of 5
   numbers (LSH banding). Files sharing at least one band hash become
   candidates.
2. Candidates are verified with Jaccard similarity estimated from the full
   512-value signature in Go.
3. At Jaccard ≥ 75% the file joins the best-matching candidate's book
   (greedy best match, no transitive merging of books); otherwise a new
   book is created.

Every file belongs to exactly one book. Grouping runs inside the scan
transaction, so results do not depend on worker scheduling.

### Book-level metadata

`books.json` picks one title, one author list and the remaining book-level
metadata per book; the per-file entries keep their original titles
(whitespace-normalized), their placeholder-filtered authors and their
identifiers with provenance. The chosen values are computed by `Consolidate`
after a successful scan and stored in the `books` and `book_*` tables; the
raw `book_files` metadata is preserved. The reports read the consolidated
book-level values; file entries read the stored per-file identifiers. All
comparisons below use normalized keys:

- **Normalization.** Comparisons use trimmed, whitespace-collapsed,
  lower-cased keys; diacritics are folded (NFD mark-stripping for Latin
  script only, so Cyrillic `й` and Indic scripts survive, plus `ø→o`,
  `ß→ss`, `æ→ae`, `œ→oe`, `ł→l`, `đ→d`, `þ→th`, `ı→i`).
- **PDF metadata.** `pdfinfo` reads the Info dictionary and, when present,
  the XMP packet. XMP wins unless the Info `ModDate` is newer. The supported
  fields map to title, authors, description (`Subject`/`dc:description`),
  subjects (`Keywords`/`dc:subject`/`pdf:Keywords`), publisher, publication
  date, series, languages and ISBN/DOI identifiers. Cover images and
  Calibre-specific custom metadata are not read.
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
- **Description.** HTML markup is converted to plain text before voting
  (tags become spaces, entities are decoded, and `script`/`style`/`head`
  contents are dropped). The most common normalized text wins; ties prefer
  the longer text. The raw `book_files.description` is preserved as
  extracted.
- **Publications.** Every distinct publisher/publication-date pair is kept,
  so different files may contribute different editions of the same book.
  Publisher names are grouped case-insensitively (diacritics folded) and
  the first spelling is kept; pairs are deduplicated and emitted in
  first-seen order. An undated occurrence does not create a separate
  blank-date entry when the same publisher already has at least one dated
  entry; if the publisher has no valid date at all, one blank-date entry is
  kept. A valid date without a publisher is kept as a pair with an empty
  publisher.
- **Publication date validation.** Dates are normalized to `YYYY`,
  `YYYY-MM` or `YYYY-MM-DD`; RFC3339/ISO datetimes are truncated to their
  date. Impossible dates, free-form values such as `circa 1999` or
  `1999. május`, and implausible years outside `1000`..current year + 1
  (for example `0101-01-01`) are dropped. A publisher with no valid date
  keeps an empty date.
- **Series.** The most common normalized series name wins. `series_index`
  is voted only among files that carry the chosen series, so an index can
  never be paired with a different series.
- **Subjects.** The union of all files' subjects, deduplicated
  case-insensitively; the first spelling and order are kept.
- **Languages.** Only valid BCP 47 tags are kept, canonicalized (for
  example `en_US` becomes `en-US`). Empty values, invalid codes such as
  `english`, and the meaningless bases `und`, `mul`, `zxx` and `mis` are
  dropped. The result is the union of the remaining tags in first-seen
  order.
- **Identifiers and source.** Every identifier carries a `source`:
  `METADATA`, `TEXT` or `METADATA_AND_TEXT`. Book-level identifiers store
  only ISBNs; file-level identifiers keep other schemes as well. Values are
  validated, ISBN-10 values are converted to ISBN-13, and the result is
  deduplicated by value. If the same ISBN comes from both metadata and
  text, it becomes one entry with `METADATA_AND_TEXT`.
- **ISBN fallback from text.** The parser always scans plain text for ISBN
  candidates, even when metadata already has an ISBN. It captures the first
  64 KiB of the linear reading order; if that yields no ISBN, it checks the
  first 64 KiB of each non-linear spine item and manifest-only textual
  resource. The end of the linear text is not scanned. The extraction
  follows the isbnx approach (ISBN markers, ISBN-10/ISBN-13 patterns,
  978/979 fallback), converts Arabic-Indic and Persian digits to ASCII,
  and validates every candidate with the same ISBN parser used for
  metadata. At most one unique ISBN-13 is added as a file-level ISBN
  identifier; the linear scan is a separate pass over the reading order.

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
- `enrich` — prepare local OpenLibrary dump data (see
  [OpenLibrary data](#openlibrary-data)).
- `help` — show help for any command.
- `completion` — generate shell autocompletion scripts.

Run `margaret --help` or `margaret scan --help` for details.

---

## OpenLibrary data

`margaret enrich` prepares the local OpenLibrary bulk data dumps that are
used to enrich scanned book metadata. The dumps are read from disk; no
OpenLibrary web API calls are made.

The dumps are published monthly on the
[OpenLibrary dumps page](https://openlibrary.org/developers/dumps) as
gzipped TSV. The three families used by Margaret are:

| Dump | Approx. compressed size |
| --- | --- |
| editions | ~9.2 GB |
| works | ~2.9 GB |
| authors | ~0.5 GB |

`enrich` searches for the dump in this order:

1. `--openlibrary-dir DIR`
2. `MARGARET_OPENLIBRARY_DIR`
3. `./data/openlibrary`
4. `$XDG_DATA_HOME/margaret/openlibrary` (default
   `~/.local/share/margaret/openlibrary`)
5. `data/openlibrary` next to the executable

If several directories hold a valid dump, the newest one wins. A combined
`ol_dump_latest.txt.gz` dump is also accepted and satisfies all three
families.

A combined dump is processed in one combined pass plus a small temporary
author sidecar; work-level metadata is deferred in that mode. The sidecar is
removed when the run finishes, but it may need about 1 GB of temporary disk.
For full work-level enrichment, the three family dumps are recommended.

Use `--status` to see the resolved paths, the ISBN coverage of the scan
database, the in-memory matching cache and the stored match status counts:

```sh
margaret enrich --status
```

The cache summary counts the books that have a usable title, ISBN or author.
Books without any of these will end up as `no_data` when matching runs.

Use `--download` to fetch missing dumps. Without it, an interactive terminal
is asked whether to download when no dump is found; scripts fail with an
error instead of downloading implicitly.

```sh
margaret enrich --download
margaret enrich --openlibrary-dir /mnt/dumps/openlibrary
```

Downloads are resumable: an interrupted transfer is kept as a `.part` file
and continued from where it stopped. Existing valid dumps are skipped.

The dumps are regenerated monthly. To move to the current month, run:

```sh
margaret enrich --refresh
```

`--refresh` re-resolves the rolling dump URLs, downloads the newer dated
files, reprocesses every book against them, and refreshes the enrichment.
Old dump files are not deleted automatically; the newest valid file in the
dump directory is used.

### License and attribution

The OpenLibrary catalog is contributed by libraries and dedicated to the
public domain. The Internet Archive does not assert new copyright or
proprietary rights over the database, and contributions are provided under
the [CC0 1.0 Universal license](https://creativecommons.org/publicdomain/zero/1.0/).
Individual records may still carry rights in some jurisdictions, so review
the [OpenLibrary licensing page](https://openlibrary.org/developers/licensing)
and the dump page before redistributing derived data. Margaret only reads
the dumps locally; it does not call the OpenLibrary web API.

`enrich` matches the scanned books against the local dump, stores the match
status, confidence and top candidates in `margaret.db`, applies OpenLibrary
metadata to confirmed books, and runs a local metadata/content conflict
check. Enriched values are stored in the separate `book_enrichment_*` tables;
the raw scan metadata is left untouched. The enrichment pass reads the dump
again, but only for the selected editions, works and authors.

By default only books without a current match are processed. `--force`
reprocesses every book and refreshes the enrichment.

### `enrich` flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--db` | `string` | `margaret.db` | SQLite database file to read. |
| `--openlibrary-dir` | `string` | `""` | Directory containing the dump files. |
| `--download` | `bool` | `false` | Download missing dumps before enriching. |
| `--refresh` | `bool` | `false` | Download the current dumps and reprocess every book (implies `--download` and `--force`). |
| `--status` | `bool` | `false` | Show resolved dump files, scan statistics and match status counts, then exit. |
| `--force` | `bool` | `false` | Reprocess every book even when a stored match is current. |
| `--enriched-out` | `string` | `reports/books_enriched.json` | Write a JSON report of confirmed enriched books. |
| `--review-out` | `string` | `reports/review.json` | Write a JSON report of books needing review. |

---

## Reports

Generated JSON reports are written under `reports/` by default, relative to
the current directory. The parent directory is created automatically. Passing
an explicit `--*-out` path writes there instead.

| Report | Default path |
| --- | --- |
| failures | `reports/failures.json` |
| skipped | `reports/skipped.json` |
| duplicates | `reports/duplicates.json` |
| grouped books | `reports/books.json` |
| confirmed enriched books | `reports/books_enriched.json` |
| books needing review | `reports/review.json` |
| search hits | `reports/search.json` |

`books_enriched.json` contains only confirmed matches. Its book fields are
merged with the OpenLibrary work/edition/author metadata where available, and
it carries `match`, `enrichment` (source, keys, matcher/dump versions) and
`author_details` objects for provenance. `review.json` groups the uncertain
books into separate arrays: `probable`, `ambiguous`, `needs_review`,
`not_found` and `no_data`. Empty categories are written as `[]`; when no book
needs review, the file is not created.

`enrich` also runs a local conflict check. Two books with the same normalized
title and author whose content MinHash Jaccard is below 75% are marked
`needs_review` with reason `metadata_content_mismatch`: the metadata claims
they are the same book, but the content says otherwise. Both books appear in
`review.json` with their peer IDs and content similarity, and they are
excluded from `books_enriched.json` even when the OpenLibrary match itself was
confirmed.

## Regenerating reports

`scan` writes the `books.json` / `duplicates.json` reports only with
`--report`, but the database always holds the full data. `report` regenerates
the book, duplicate, enriched and review reports from an existing database
without scanning — after a report-less scan, when the JSON files were
deleted, or when they are wanted at different paths:

```sh
margaret report
margaret report --db /tmp/test.db --books-out /tmp/books.json
```

`report` reads the consolidated book-level metadata but does not recompute
it; the values come from the last successful scan. Databases created before
the metadata columns existed need a fresh scan (`--fresh`) to populate them.
Databases containing 128-value MinHash signatures from older versions also
need a fresh scan; `--resume` refuses them because the signature length no
longer matches the 512-value format.

`failures.json` and `skipped.json` cannot be regenerated: they are produced
during the scan and are not stored in the database. Against an empty or
missing database the command fails with an error pointing to `scan`, instead
of writing empty reports or creating an empty database file.

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
`reports/search.json`):

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
- The scan can be interrupted with `Ctrl-C`; it cancels gracefully, keeps the
  files processed so far, marks the run as canceled and writes the failures
  report before exiting.
- A second `Ctrl-C` forces an immediate exit with code 130 if an in-flight
  operation cannot be canceled. This is the escape hatch for a parser or
  archive extractor stuck outside the cancellation path.

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

---

## License

MIT — see [LICENSE](LICENSE).