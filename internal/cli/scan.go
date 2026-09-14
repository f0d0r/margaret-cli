package cli

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/processor"
	"github.com/f0d0r/margaret-cli/internal/report"
	"github.com/spf13/cobra"
)

var (
	scanWorkers     int
	archiveDepth    int
	archivePassword string
	spoolMemLimit   int64
	failuresOutPath string
	ftpUser         string
	ftpPass         string
	duplicatesOut   string
	booksOut        string
	reportEnabled   bool
	freshFlag       bool
	resumeFlag      bool
	dbPath          string
)

// scanCmd scans a directory for ebook files.
var scanCmd = &cobra.Command{
	Use:   "scan <path>",
	Short: "Scan a directory for ebook files",
	Long: `Recursively scans the given directory and reads the author
and title metadata of every ebook found. Ebooks are also searched
inside zip, tar, gz, bz2, rar and 7z archives (nested archives are
unpacked up to the depth given with --archive-depth).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := processor.DefaultScanProcessorConfig()
		if cmd.Flags().Changed("workers") {
			cfg.ScanWorkers = scanWorkers
			cfg.ParseWorkers = scanWorkers
			cfg.QueueSize = scanWorkers * 2
		}
		if cmd.Flags().Changed("archive-depth") {
			cfg.MaxDepth = archiveDepth
		}
		if cmd.Flags().Changed("spool-mem-limit") {
			cfg.SpoolMemLimit = spoolMemLimit
		}
		cfg.Password = archivePassword
		cfg.FTPUser = ftpUser
		cfg.FTPPass = ftpPass
		return runScan(cmd.Context(), args[0], cfg)
	},
}

func init() {
	scanCmd.Flags().IntVar(&scanWorkers, "workers", 0, "number of worker goroutines (default: number of CPUs)")
	scanCmd.Flags().IntVar(&archiveDepth, "archive-depth", 2, "maximum archive nesting depth to unpack")
	scanCmd.Flags().StringVar(&archivePassword, "archive-password", "", "password for encrypted rar and 7z archives")
	scanCmd.Flags().Int64Var(&spoolMemLimit, "spool-mem-limit", 0, "max bytes per member buffered in memory before spilling to a temp file (0 = 64 MiB default)")
	scanCmd.Flags().StringVar(&failuresOutPath, "failures-out", "failures.json", "write a JSON report of the failed items to this file")
	scanCmd.Flags().StringVar(&ftpUser, "ftp-user", "", "FTP username (default: anonymous)")
	scanCmd.Flags().StringVar(&ftpPass, "ftp-pass", "", "FTP password (default: anonymous)")
	scanCmd.Flags().StringVar(&duplicatesOut, "duplicates-out", "duplicates.json", "write a JSON report of the duplicate books to this file")
	scanCmd.Flags().StringVar(&booksOut, "books-out", "books.json", "write a JSON report of the grouped books to this file")
	scanCmd.Flags().BoolVar(&reportEnabled, "report", false, "write the books and duplicates JSON reports (failures are always written)")
	scanCmd.Flags().BoolVar(&freshFlag, "fresh", false, "delete existing scan data and start from a clean slate")
	scanCmd.Flags().BoolVar(&resumeFlag, "resume", false, "keep existing scan data and only process new or changed files")
	scanCmd.Flags().StringVar(&dbPath, "db", database.DefaultPath, "SQLite database file to use (use \":memory:\" for an ephemeral database)")
	rootCmd.AddCommand(scanCmd)
}

func runScan(ctx context.Context, root string, cfg processor.ScanProcessorConfig) error {
	start := time.Now()

	if dbPath == "" {
		return fmt.Errorf("database path must not be empty")
	}
	conn, err := database.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)

	mode, err := resolveScanMode(ctx, q, root)
	if err != nil {
		return err
	}
	if mode == modeFresh {
		// Each fresh scan starts with a clean slate so re-running a scan
		// never mixes results from previous runs. The schema and migration
		// history are kept.
		if err := database.Clear(conn); err != nil {
			return fmt.Errorf("clear database %q: %w", dbPath, err)
		}
	}
	cfg.Resume = mode == modeResume

	runID, err := q.CreateScanRun(ctx, db.CreateScanRunParams{
		Root:      root,
		Mode:      mode.String(),
		StartedAt: time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("record scan run: %w", err)
	}

	var bar *progressBar
	var rep *progressReporter
	// Force-enable for testing in non-interactive environments
	bar = newProgressBar()
	rep = &progressReporter{bar: bar}
	cfg.OnProgress = func(p processor.Progress) {
		if rep != nil {
			rep.report(p)
		}
	}

	var proc processor.Processor = processor.NewScanProcessor(conn, q, cfg, root)

	// A ticker keeps the bar animating (spinner + in-flight item names) while
	// a long-running unit of work emits no progress event of its own.
	stop := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if rep != nil {
					rep.tick(proc.Stats())
				}
			}
		}
	}()

	err = proc.Process(ctx)
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if ferr := q.FinishScanRun(ctx, db.FinishScanRunParams{
		Status:     status,
		FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		ID:         runID,
	}); ferr != nil {
		return fmt.Errorf("finish scan run: %w", ferr)
	}
	close(stop)
	<-tickDone
	stats := proc.Stats()
	failures := proc.Failures()
	if rep != nil {
		rep.sync(stats)
	}
	if bar != nil {
		bar.finish()
	}

	printReport(stats, time.Since(start))
	if err := report.WriteFailures(failures, failuresOutPath); err != nil {
		return err
	}
	if reportEnabled {
		if err := report.WriteDuplicates(q, duplicatesOut); err != nil {
			return err
		}
		if err := report.WriteBooks(q, booksOut); err != nil {
			return err
		}
	}
	return err
}

func printReport(s processor.Progress, d time.Duration) {
	succeeded := s.Parsed
	total := succeeded + s.Failed + s.Skipped
	fmt.Printf("Total      %d ebook(s)\n", total)
	fmt.Printf("Succeeded  %d\n", succeeded)
	fmt.Printf("Failed     %d\n", s.Failed)
	if s.Skipped > 0 {
		fmt.Printf("Skipped    %d\n", s.Skipped)
	}
	fmt.Printf("Duration   %s\n", d.Round(time.Millisecond))
}
