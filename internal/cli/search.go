package cli

import (
	"context"
	"fmt"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/search"
	"github.com/spf13/cobra"
)

var (
	author   bool
	title    bool
	limit    int
	jsonFlag bool
	jsonOut  string
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search for books and authors in the database",
	Long: `Search for books and authors in the database using full-text search.
By default both titles and authors are searched, ordered by relevance.
With --json the hits are written in the books.json shape to the file
given with --json-out.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSearch(cmd.Context(), args[0])
	},
}

func init() {
	searchCmd.Flags().StringVar(&dbPath, "db", database.DefaultPath, "SQLite database file to read")
	searchCmd.Flags().BoolVar(&author, "author", false, "Search for authors")
	searchCmd.Flags().BoolVar(&title, "title", false, "Search for titles")
	searchCmd.Flags().IntVar(&limit, "limit", 0, "Limit the number of results (0 = unlimited)")
	searchCmd.Flags().BoolVar(&jsonFlag, "json", false, "Write results in JSON format")
	searchCmd.Flags().StringVar(&jsonOut, "json-out", "search.json", "write the JSON results to this file")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(ctx context.Context, searchQuery string) error {
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
	if err := search.Search(ctx, q, author, title, limit, jsonFlag, jsonOut, searchQuery); err != nil {
		return err
	}
	return nil
}
