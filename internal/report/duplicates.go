package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/f0d0r/margaret-cli/internal/db"
)

// duplicateReport is the JSON representation of one original book together
// with every other path that holds the same content (same hash).
type duplicateReport struct {
	Authors    string   `json:"authors"`
	Title      string   `json:"title"`
	Path       string   `json:"path"`
	Duplicates []string `json:"duplicates"`
}

// WriteDuplicates writes a JSON report of the duplicate books found during
// the scan to path. When no duplicates were found nothing is written, so no
// empty file is left behind.
func WriteDuplicates(q *db.Queries, path string) error {
	rows, err := q.ListBookFileDuplicates(context.Background())
	if err != nil {
		return fmt.Errorf("list duplicates: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	report := make([]duplicateReport, 0, len(rows))
	for _, r := range rows {
		if len(report) == 0 || report[len(report)-1].Path != r.Path {
			report = append(report, duplicateReport{
				Authors: r.Authors,
				Title:   r.Title,
				Path:    r.Path,
			})
		}
		last := &report[len(report)-1]
		last.Duplicates = append(last.Duplicates, r.DuplicatePath)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write duplicate report: %w", err)
	}
	return nil
}
