package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/processor"
)

func TestRunReportMissingDB(t *testing.T) {
	dir := t.TempDir()
	oldDB := dbPath
	t.Cleanup(func() { dbPath = oldDB })
	dbPath = filepath.Join(dir, "nope.db")

	if err := runReport(context.Background()); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected run-scan-first error, got %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("report must not create a missing database, stat err: %v", err)
	}
}

func TestRunReportEmptyDB(t *testing.T) {
	dir := t.TempDir()
	oldDB := dbPath
	t.Cleanup(func() { dbPath = oldDB })
	dbPath = filepath.Join(dir, "empty.db")

	conn, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if err := runReport(context.Background()); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected run-scan-first error, got %v", err)
	}
}

func TestRunReportRegeneratesSameOutput(t *testing.T) {
	booksDir, _, _ := fileScanSetup(t, false, false, false)
	reportEnabled = true

	// Full scan with reports: the reference output.
	if err := runScan(context.Background(), booksDir, processor.DefaultScanProcessorConfig()); err != nil {
		t.Fatal(err)
	}
	refBooks, err := os.ReadFile(booksOut)
	if err != nil {
		t.Fatal(err)
	}
	refDups, err := os.ReadFile(duplicatesOut)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the reports and regenerate them from the database alone.
	if err := os.Remove(booksOut); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(duplicatesOut); err != nil {
		t.Fatal(err)
	}
	if err := runReport(context.Background()); err != nil {
		t.Fatal(err)
	}

	gotBooks, err := os.ReadFile(booksOut)
	if err != nil {
		t.Fatal(err)
	}
	gotDups, err := os.ReadFile(duplicatesOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBooks) != string(refBooks) {
		t.Errorf("books report differs from scan --report output")
	}
	if string(gotDups) != string(refDups) {
		t.Errorf("duplicates report differs from scan --report output")
	}
}
