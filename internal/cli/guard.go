package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/f0d0r/margaret-cli/internal/db"
)

// requireDatabaseFile fails when path does not exist. Read-only commands
// (report, and later search) must never create a missing database file via
// open-or-create: a missing file means no scan has produced it yet.
func requireDatabaseFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("database %q does not exist: run \"margaret-cli scan <path>\" first", path)
	}
	return fmt.Errorf("stat database %q: %w", path, err)
}

// requireScannedData fails when the database holds no scanned files. An
// empty report (or an empty search result) would mislead: it looks like "no
// books found" when really "no scan has run". Shared by every read-only
// command so the message stays consistent.
func requireScannedData(ctx context.Context, q *db.Queries, path string) error {
	n, err := q.CountBookFiles(ctx)
	if err != nil {
		return fmt.Errorf("count recorded files: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("database %q holds no scanned data: run \"margaret-cli scan <path>\" first", path)
	}
	return nil
}
