package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/f0d0r/margaret-tools/internal/db"
	"golang.org/x/term"
)

// scanMode is the user's declared intent for a scan over existing data.
type scanMode int

const (
	modeFresh scanMode = iota
	modeResume
)

func (m scanMode) String() string {
	if m == modeResume {
		return "resume"
	}
	return "fresh"
}

// stdinIsTerminal reports whether stdin is an interactive terminal. It is a
// variable (not a direct call) so tests can stub non-interactive runs.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// resolveScanMode decides whether the scan starts from a clean slate or
// continues existing data, following the hybrid model: explicit flags win,
// an interactive terminal is asked, and a non-interactive run without flags
// fails instead of guessing.
func resolveScanMode(ctx context.Context, q *db.Queries, root string) (scanMode, error) {
	if freshFlag && resumeFlag {
		return modeFresh, fmt.Errorf("--fresh and --resume are mutually exclusive")
	}
	files, err := q.CountBookFiles(ctx)
	if err != nil {
		return modeFresh, fmt.Errorf("count recorded files: %w", err)
	}
	if files == 0 {
		// Nothing recorded yet: there is nothing to decide. An explicit
		// --resume is accepted (it simply finds nothing to skip).
		if resumeFlag {
			return modeResume, nil
		}
		return modeFresh, nil
	}
	if freshFlag {
		return modeFresh, nil
	}
	if resumeFlag {
		return modeResume, checkSameRoot(ctx, q, root)
	}
	if !stdinIsTerminal() {
		return modeFresh, fmt.Errorf("database %q already holds scanned data: pass --resume to continue or --fresh to start over", dbPath)
	}
	return promptScanMode(ctx, q, root)
}

// checkSameRoot rejects resuming a different tree in non-interactive mode,
// where nobody can answer the warning. On a terminal the caller (--resume)
// is explicit, so a printed warning is enough.
func checkSameRoot(ctx context.Context, q *db.Queries, root string) error {
	prev, err := latestRun(ctx, q)
	if err != nil || prev == nil || prev.Root == root {
		return err
	}
	if stdinIsTerminal() {
		fmt.Fprintf(os.Stderr, "WARNING: resuming a different tree: database was last scanned from %q, now %q. Results will mix both trees; use --fresh or a different --db file to avoid that.\n", prev.Root, root)
		return nil
	}
	return fmt.Errorf("database was last scanned from %q, not %q: refusing to mix two trees without a terminal; use --fresh or a different --db file", prev.Root, root)
}

// promptScanMode asks whether to resume onto or wipe the existing data. The
// default is resume: the non-destructive choice.
func promptScanMode(ctx context.Context, q *db.Queries, root string) (scanMode, error) {
	files, err := q.CountBookFiles(ctx)
	if err != nil {
		return modeFresh, fmt.Errorf("count recorded files: %w", err)
	}
	books, err := q.CountBooks(ctx)
	if err != nil {
		return modeFresh, fmt.Errorf("count recorded books: %w", err)
	}
	fmt.Printf("Database %q already holds %d book(s) in %d file(s).\n", dbPath, books, files)
	prev, err := latestRun(ctx, q)
	if err != nil {
		return modeFresh, err
	}
	switch {
	case prev == nil:
		fmt.Printf("Previous scan: unknown.\n")
	case prev.Root != root:
		fmt.Printf("Previous scan: %q (%s).\n", prev.Root, formatUnix(prev.StartedAt))
		fmt.Printf("WARNING: %q is a different tree; resuming will mix both trees. Use fresh to start over, or a different --db file.\n", root)
	default:
		fmt.Printf("Previous scan: %q (%s).\n", prev.Root, formatUnix(prev.StartedAt))
	}
	fmt.Printf("Resume the scan, or delete existing data and start fresh?\n")
	fmt.Printf("  [r]esume (default): only never-seen paths are processed. Recorded\n")
	fmt.Printf("    files are skipped; failures are retried on every run. Use fresh\n")
	fmt.Printf("    to reprocess everything.\n")
	fmt.Printf("  [f]resh: delete existing data and start from a clean slate.\n")
	fmt.Printf("  [r]esume (default) / [f]resh: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return modeFresh, fmt.Errorf("read answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "r", "resume":
		return modeResume, checkSameRoot(ctx, q, root)
	case "f", "fresh":
		return modeFresh, nil
	default:
		return modeFresh, fmt.Errorf("unknown choice %q: expected resume or fresh", strings.TrimSpace(line))
	}
}

// latestRun returns the most recent scan run, or nil when no run was
// recorded yet (e.g. data written before run tracking existed).
func latestRun(ctx context.Context, q *db.Queries) (*db.ScanRun, error) {
	prev, err := q.GetLatestScanRun(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read latest scan run: %w", err)
	}
	return &prev, nil
}

func formatUnix(sec int64) string {
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04")
}
