package cli

import (
	"context"
	"fmt"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/report"
	"github.com/spf13/cobra"
)

// reportCmd regenerates the JSON reports from an existing database.
var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Regenerate JSON reports from the database",
	Long: `Regenerates the books and duplicates JSON reports from an existing
database without scanning. Useful after a scan that ran without --report,
or when the report files were deleted or are wanted at different paths.

Failures cannot be regenerated: they are produced during the scan and are
not stored in the database.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReport(cmd.Context())
	},
}

func init() {
	reportCmd.Flags().StringVar(&dbPath, "db", database.DefaultPath, "SQLite database file to read")
	reportCmd.Flags().StringVar(&duplicatesOut, "duplicates-out", "duplicates.json", "write a JSON report of the duplicate books to this file")
	reportCmd.Flags().StringVar(&booksOut, "books-out", "books.json", "write a JSON report of the grouped books to this file")
	rootCmd.AddCommand(reportCmd)
}

func runReport(ctx context.Context) error {
	if dbPath == "" {
		return fmt.Errorf("database path must not be empty")
	}
	if err := requireDatabaseFile(dbPath); err != nil {
		return err
	}
	conn, err := database.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	if err := requireScannedData(ctx, q, dbPath); err != nil {
		return err
	}
	if err := report.WriteDuplicates(q, duplicatesOut); err != nil {
		return err
	}
	if err := report.WriteBooks(q, booksOut); err != nil {
		return err
	}
	return nil
}
