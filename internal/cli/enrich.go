package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/conflict"
	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/enrich"
	"github.com/f0d0r/margaret-cli/internal/matcher"
	"github.com/f0d0r/margaret-cli/internal/openlibrary"
	"github.com/f0d0r/margaret-cli/internal/report"
	"github.com/spf13/cobra"
)

var (
	enrichOpenLibraryDir string
	enrichDownloadFlag   bool
	enrichRefreshFlag    bool
	enrichStatusFlag     bool
	enrichForceFlag      bool
	enrichEnrichedOut    string
	enrichReviewOut      string
)

// enrichCmd prepares the local OpenLibrary dumps used for metadata
// enrichment.
var enrichCmd = &cobra.Command{
	Use:   "enrich",
	Short: "Prepare local OpenLibrary data for metadata enrichment",
	Long: `Prepares and validates the local OpenLibrary bulk data dumps used to
enrich scanned book metadata. The dumps are read from disk; no OpenLibrary
web API calls are made.

The dump search checks --openlibrary-dir, MARGARET_OPENLIBRARY_DIR, then
./data/openlibrary, the per-user data directory and the directory next to the
executable. Use --status to see which files would be used. Use --download to
fetch missing dumps, or accept the interactive prompt when no dump is found.
Use --refresh to fetch the current monthly dumps and reprocess every book.

The command matches scanned books against the local dump, stores the match
status, confidence and top candidates, applies OpenLibrary metadata to
confirmed books and runs a local metadata/content conflict check. By default
only books without a current match are processed; use --force to reprocess
every book.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runEnrich(cmd.Context())
	},
}

func init() {
	enrichCmd.Flags().StringVar(&dbPath, "db", database.DefaultPath, "SQLite database file to read")
	enrichCmd.Flags().StringVar(&enrichOpenLibraryDir, "openlibrary-dir", "", "directory containing the OpenLibrary dump files")
	enrichCmd.Flags().BoolVar(&enrichDownloadFlag, "download", false, "download missing OpenLibrary dumps before enriching")
	enrichCmd.Flags().BoolVar(&enrichRefreshFlag, "refresh", false, "download the current OpenLibrary dumps and reprocess every book (implies --download and --force)")
	enrichCmd.Flags().BoolVar(&enrichStatusFlag, "status", false, "show the resolved dump files and scan database statistics, then exit")
	enrichCmd.Flags().BoolVar(&enrichForceFlag, "force", false, "reprocess every book even when a stored match is current")
	enrichCmd.Flags().StringVar(&enrichEnrichedOut, "enriched-out", report.DefaultPath("books_enriched.json"), "write a JSON report of confirmed enriched books to this file")
	enrichCmd.Flags().StringVar(&enrichReviewOut, "review-out", report.DefaultPath("review.json"), "write a JSON report of books needing review to this file")
	rootCmd.AddCommand(enrichCmd)
}

// newOpenLibraryDownloader is a seam for tests that need to point the
// downloader at a local HTTP server.
var newOpenLibraryDownloader = func(dir string) *openlibrary.Downloader {
	return &openlibrary.Downloader{Dir: dir}
}

func runEnrich(ctx context.Context) error {
	if enrichStatusFlag && (enrichDownloadFlag || enrichRefreshFlag) {
		return errors.New("--status, --download and --refresh are mutually exclusive")
	}
	if enrichStatusFlag {
		return runEnrichStatus(ctx)
	}

	dataset, err := resolveOrDownloadDataset(ctx)
	if err != nil {
		return err
	}
	if dataset == nil {
		return nil
	}
	printDatasetWarnings(dataset)
	printDataset(dataset)

	ready, err := databaseReady(dbPath)
	if err != nil {
		return err
	}
	if !ready {
		if enrichDownloadFlag || enrichRefreshFlag {
			fmt.Println("OpenLibrary data ready.")
			return nil
		}
		return fmt.Errorf("database %q does not exist: run \"margaret scan <path>\" first", dbPath)
	}

	conn, err := database.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer func() {
		_ = conn.Close()
	}()
	q := db.New(conn)
	if err := requireScannedData(ctx, q, dbPath); err != nil {
		return err
	}

	matcherVersion := matcher.Version
	dumpVersion := matcher.DatasetVersion(dataset)
	cache, err := matcher.Build(ctx, q, matcher.Options{
		Pending:        !enrichForceFlag && !enrichRefreshFlag,
		MatcherVersion: matcherVersion,
		DumpVersion:    dumpVersion,
	})
	if err != nil {
		return fmt.Errorf("build matching cache: %w", err)
	}
	if cache.Stats.Books == 0 {
		fmt.Println("No books need matching.")
		if err := runConflictDetection(ctx, conn, q); err != nil {
			return err
		}
		if err := runEnrichment(ctx, dataset, conn, q, matcherVersion, dumpVersion); err != nil {
			return err
		}
		if err := writeEnrichmentReports(ctx, q); err != nil {
			return err
		}
		return printMatchStatusCounts(ctx, q)
	}

	fmt.Printf("Matching %d book(s)...\n", cache.Stats.Books)
	stream, err := matcher.StreamDataset(ctx, dataset, cache, matcher.StreamOptions{
		Progress: printStreamProgress,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	for _, warning := range stream.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}

	ranked, err := matcher.Rank(stream, cache, matcher.RankOptions{})
	if err != nil {
		return err
	}
	if err := matcher.Persist(ctx, conn, q, ranked, matcher.PersistMeta{
		MatcherVersion: matcherVersion,
		DumpVersion:    dumpVersion,
	}, matcher.PersistOptions{}); err != nil {
		return err
	}
	if err := runConflictDetection(ctx, conn, q); err != nil {
		return err
	}
	if err := runEnrichment(ctx, dataset, conn, q, matcherVersion, dumpVersion); err != nil {
		return err
	}
	if err := writeEnrichmentReports(ctx, q); err != nil {
		return err
	}
	return printMatchStatusCounts(ctx, q)
}

func runEnrichment(ctx context.Context, dataset *openlibrary.Dataset, conn *sql.DB, q *db.Queries, matcherVersion, dumpVersion string) error {
	result, err := enrich.Enrich(ctx, dataset, conn, q, enrich.Options{
		MatcherVersion: matcherVersion,
		DumpVersion:    dumpVersion,
		Force:          enrichForceFlag || enrichRefreshFlag,
	})
	if err != nil {
		return fmt.Errorf("enrich metadata: %w", err)
	}
	if result.Targets > 0 {
		fmt.Printf("Enriched %d/%d book(s)\n", result.Enriched, result.Targets)
	}
	return nil
}

func runConflictDetection(ctx context.Context, conn *sql.DB, q *db.Queries) error {
	result, err := conflict.DetectAndPersist(ctx, conn, q, conflict.Options{})
	if err != nil {
		return fmt.Errorf("detect conflicts: %w", err)
	}
	if result.Stats.Conflicts > 0 {
		fmt.Printf("Conflicts: %d pair(s), %d book(s)\n",
			result.Stats.Conflicts, result.Stats.ConflictBooks)
	}
	return nil
}

func writeEnrichmentReports(ctx context.Context, q *db.Queries) error {
	if err := report.WriteEnrichedBooks(ctx, q, enrichEnrichedOut); err != nil {
		return err
	}
	if err := report.WriteReview(ctx, q, enrichReviewOut); err != nil {
		return err
	}
	return nil
}

func resolveOrDownloadDataset(ctx context.Context) (*openlibrary.Dataset, error) {
	dataset, err := openlibrary.Resolve(enrichOpenLibraryDir)
	if err != nil {
		var notFound *openlibrary.NotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
		if !enrichDownloadFlag && !enrichRefreshFlag {
			dir, dirErr := openlibrary.DownloadDir(enrichOpenLibraryDir)
			if dirErr != nil {
				return nil, dirErr
			}
			if !stdinIsTerminal() {
				return nil, noDumpError(notFound.Candidates)
			}
			ok, promptErr := promptDownload(ctx, os.Stdin, notFound.Candidates, dir, openlibrary.Missing(dir))
			if promptErr != nil {
				return nil, promptErr
			}
			if !ok {
				fmt.Println("No changes made.")
				return nil, nil
			}
		}
	}

	download := enrichDownloadFlag || enrichRefreshFlag || dataset == nil
	if download {
		dir, dirErr := chooseDownloadDir(dataset)
		if dirErr != nil {
			return nil, dirErr
		}
		fmt.Printf("Downloading OpenLibrary dumps to %s\n", dir)
		results, downloadErr := downloadDumps(ctx, dir)
		if downloadErr != nil {
			return nil, downloadErr
		}
		printDownloadResults(results)
		dataset, err = openlibrary.Resolve(enrichOpenLibraryDir)
		if err != nil {
			return nil, err
		}
	}

	if dataset == nil {
		return nil, errors.New("no OpenLibrary dump available")
	}
	return dataset, nil
}

func databaseReady(path string) (bool, error) {
	if path == "" {
		return false, errors.New("database path must not be empty")
	}
	if path == ":memory:" {
		return true, nil
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("stat database %q: %w", path, err)
	}
}

func printStreamProgress(progress matcher.StreamProgress) {
	line := fmt.Sprintf("%s: lines=%d records=%d candidates=%d",
		progress.Pass, progress.Lines, progress.Records, progress.Candidates)
	if progress.Pending > 0 {
		line += fmt.Sprintf(" pending=%d", progress.Pending)
	}
	fmt.Fprintf(os.Stderr, "\r%s", line)
}

func printMatchStatusCounts(ctx context.Context, q *db.Queries) error {
	rows, err := q.CountBooksByMatchStatus(ctx)
	if err != nil {
		return fmt.Errorf("count match statuses: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	fmt.Println("Match status:")
	for _, status := range []string{
		"confirmed", "probable", "ambiguous", "needs_review",
		"not_found", "no_data", "unmatched",
	} {
		fmt.Printf("  %-12s %d\n", status, counts[status])
	}
	return nil
}

func chooseDownloadDir(dataset *openlibrary.Dataset) (string, error) {
	if dataset != nil && dataset.Dir != "" {
		return dataset.Dir, nil
	}
	return openlibrary.DownloadDir(enrichOpenLibraryDir)
}

func downloadDumps(ctx context.Context, dir string) ([]openlibrary.DownloadResult, error) {
	downloader := newOpenLibraryDownloader(dir)
	downloader.Progress = printDownloadProgress
	downloader.Refresh = enrichRefreshFlag
	results, err := downloader.Ensure(ctx, nil)
	fmt.Fprintln(os.Stderr)
	return results, err
}

func printDownloadProgress(kind openlibrary.Kind, done, total int64) {
	if total > 0 {
		fmt.Fprintf(os.Stderr, "\r%s: %s / %s (%d%%)",
			kind, openlibrary.FormatSize(done), openlibrary.FormatSize(total), done*100/total)
		return
	}
	fmt.Fprintf(os.Stderr, "\r%s: %s", kind, openlibrary.FormatSize(done))
}

func printDownloadResults(results []openlibrary.DownloadResult) {
	for _, result := range results {
		state := "downloaded"
		if result.Skipped {
			state = "already present"
		}
		fmt.Printf("  %-8s %s (%s, %s)\n",
			result.Kind, result.Path, state, openlibrary.FormatSize(result.Bytes))
	}
}

func printDatasetWarnings(dataset *openlibrary.Dataset) {
	for _, warning := range dataset.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
}

func printDataset(dataset *openlibrary.Dataset) {
	fmt.Printf("OpenLibrary data: %s\n", dataset.Dir)
	for _, file := range dataset.Files() {
		version := file.Version
		if version == "" || version == "latest" {
			version = file.Date().Format("2006-01-02")
		}
		fmt.Printf("  %-8s %-42s %10s  %s\n",
			file.Kind, filepath.Base(file.Path), openlibrary.FormatSize(file.Size), version)
	}
	for _, invalid := range dataset.Invalid {
		fmt.Printf("  %-8s %-42s %s\n", "invalid", filepath.Base(invalid.Path), invalid.Err)
	}
	if dataset.EditionsFromCombined() {
		fmt.Println("  mode: combined dump (works metadata deferred)")
	}
}

func runEnrichStatus(ctx context.Context) error {
	databaseErr := printScanDatabaseStatus(ctx)

	candidates := openlibrary.Candidates(enrichOpenLibraryDir)
	fmt.Println("OpenLibrary dump search:")
	for _, candidate := range candidates {
		printCandidateLine(candidate)
	}

	selected, err := openlibrary.Select(candidates)
	if err != nil {
		fmt.Println("\nNo usable OpenLibrary dump found.")
		fmt.Println(`Run "margaret enrich --download" or pass --openlibrary-dir DIR.`)
		return databaseErr
	}
	fmt.Printf("\nSelected: %s (%s)\n", selected.Dir, selected.Origin)
	printDataset(selected.Dataset)
	return databaseErr
}

func printScanDatabaseStatus(ctx context.Context) error {
	if dbPath == "" {
		fmt.Println("Scan database: (not configured)")
		return nil
	}
	if dbPath == ":memory:" {
		fmt.Printf("Scan database: %s (ephemeral)\n", dbPath)
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Scan database: %s (missing)\n", dbPath)
			return nil
		}
		return fmt.Errorf("stat database %q: %w", dbPath, err)
	}

	conn, err := database.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	queries := db.New(conn)
	books, err := queries.CountBooks(ctx)
	if err != nil {
		return fmt.Errorf("count books: %w", err)
	}
	files, err := queries.CountBookFiles(ctx)
	if err != nil {
		return fmt.Errorf("count book files: %w", err)
	}
	isbn, err := queries.CountBooksWithISBN(ctx)
	if err != nil {
		return fmt.Errorf("count ISBNs: %w", err)
	}

	fmt.Printf("Scan database: %s\n", dbPath)
	fmt.Printf("  books: %d\n", books)
	fmt.Printf("  files: %d\n", files)
	fmt.Printf("  books with ISBN: %d", isbn.Books)
	if books > 0 {
		fmt.Printf(" (%s)", formatPercent(isbn.Books, books))
	}
	fmt.Println()
	fmt.Printf("  distinct ISBNs: %d\n", isbn.Isbns)
	fmt.Printf("  books without ISBN: %d\n", books-isbn.Books)
	if books == 0 {
		fmt.Println(`  no scanned data (run "margaret scan <path>" first)`)
		return nil
	}

	cache, err := matcher.Build(ctx, queries, matcher.Options{})
	if err != nil {
		return fmt.Errorf("build matching cache: %w", err)
	}
	fmt.Printf("  matching cache: %d books (%d with title, %d with ISBN, %d with authors)\n",
		cache.Stats.Books, cache.Stats.WithTitle, cache.Stats.WithISBN, cache.Stats.WithAuthors)
	return printMatchStatusCounts(ctx, queries)
}

func printCandidateLine(candidate openlibrary.Candidate) {
	status := "[ok]"
	switch {
	case candidate.Err != nil && os.IsNotExist(candidate.Err):
		status = "[missing]"
	case candidate.Err != nil:
		status = "[error]"
	case candidate.Dataset != nil && !candidate.Usable() && len(candidate.Dataset.Invalid) > 0:
		status = "[invalid]"
	case candidate.Dataset == nil || len(candidate.Dataset.Files()) == 0:
		status = "[no dump]"
	case !candidate.Usable():
		status = "[incomplete]"
	}

	fmt.Printf("  %-12s %s (%s)", status, candidate.Dir, candidate.Origin)
	if candidate.Err != nil && !os.IsNotExist(candidate.Err) {
		fmt.Printf(": %v", candidate.Err)
	}
	fmt.Println()
}

func promptDownload(ctx context.Context, in io.Reader, candidates []openlibrary.Candidate, dir string, missing []openlibrary.Kind) (bool, error) {
	fmt.Println("No OpenLibrary dump found. Searched:")
	for _, candidate := range candidates {
		printCandidateLine(candidate)
	}
	if len(missing) == 0 {
		missing = openlibrary.DefaultKinds
	}
	var parts []string
	for _, kind := range missing {
		parts = append(parts, fmt.Sprintf("%s (~%s)", kind, openlibrary.FormatSize(openlibrary.ApproximateSize[kind])))
	}
	fmt.Printf("\nDownload the latest OpenLibrary dumps to\n%s?\n", dir)
	fmt.Printf("  missing: %s\n", strings.Join(parts, ", "))
	fmt.Printf("  total:   ~%s\n", openlibrary.FormatSize(openlibrary.ApproximateTotal(missing)))
	fmt.Print("  [y/N]: ")

	answer, err := readAnswer(ctx, in)
	if err != nil {
		return false, fmt.Errorf("read answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func noDumpError(candidates []openlibrary.Candidate) error {
	var message strings.Builder
	message.WriteString("no OpenLibrary dump found; searched:\n")
	for _, candidate := range candidates {
		fmt.Fprintf(&message, "  %s (%s)", candidate.Dir, candidate.Origin)
		if candidate.Err != nil && !os.IsNotExist(candidate.Err) {
			fmt.Fprintf(&message, ": %v", candidate.Err)
		}
		message.WriteString("\n")
	}
	message.WriteString("pass --openlibrary-dir DIR or --download")
	return errors.New(message.String())
}
