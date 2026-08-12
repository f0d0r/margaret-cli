package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/f0d0r/margaret-tools/internal/parser"
	"github.com/f0d0r/margaret-tools/internal/processor"
	"github.com/spf13/cobra"
)

var (
	scanWorkers     int
	archiveDepth    int
	archivePassword string
	failuresOutPath string
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
		cfg := processor.DefaultConfig()
		if cmd.Flags().Changed("workers") {
			cfg.ScanWorkers = scanWorkers
			cfg.ParseWorkers = scanWorkers
			cfg.QueueSize = scanWorkers * 2
		}
		if cmd.Flags().Changed("archive-depth") {
			cfg.MaxDepth = archiveDepth
		}
		cfg.Password = archivePassword
		return runScan(cmd.Context(), args[0], cfg)
	},
}

func init() {
	scanCmd.Flags().IntVar(&scanWorkers, "workers", 0, "number of worker goroutines (default: number of CPUs)")
	scanCmd.Flags().IntVar(&archiveDepth, "archive-depth", 2, "maximum archive nesting depth to unpack")
	scanCmd.Flags().StringVar(&archivePassword, "archive-password", "", "password for encrypted rar and 7z archives")
	scanCmd.Flags().StringVar(&failuresOutPath, "failures-out", "failures.json", "write a JSON report of the failed items to this file")
	rootCmd.AddCommand(scanCmd)
}

func runScan(ctx context.Context, root string, cfg processor.Config) error {
	start := time.Now()

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

	proc := processor.New(parser.Dummy{}, cfg)

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

	failures, err := proc.Process(ctx, root)
	close(stop)
	<-tickDone
	stats := proc.Stats()
	if rep != nil {
		rep.sync(stats)
	}
	if bar != nil {
		bar.finish()
	}

	printReport(stats, time.Since(start))
	if err := writeFailures(failures, failuresOutPath); err != nil {
		return err
	}
	return err
}

func printReport(s processor.Progress, d time.Duration) {
	succeeded := s.Parsed
	total := succeeded + s.Failed
	fmt.Printf("Total      %d ebook(s)\n", total)
	fmt.Printf("Succeeded  %d\n", succeeded)
	fmt.Printf("Failed     %d\n", s.Failed)
	fmt.Printf("Duration   %s\n", d.Round(time.Millisecond))
}
