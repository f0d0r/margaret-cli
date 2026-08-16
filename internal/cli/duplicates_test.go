package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/database"
	"github.com/f0d0r/margaret-tools/internal/db"
)

func TestWriteDuplicatesEmpty(t *testing.T) {
	conn, cleanup, err := database.OpenTemp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	path := filepath.Join(t.TempDir(), "duplicates.json")
	if err := writeDuplicates(conn, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written, stat err = %v", err)
	}
}

func TestWriteDuplicates(t *testing.T) {
	conn, cleanup, err := database.OpenTemp()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	q := db.New(conn)
	ctx := context.Background()

	id, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "deadbeef",
		Path:  "books/mobydick.epub",
		Title: "Moby Dick",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Herman Melville"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Herman Melville")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: id,
		AuthorID:   author.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"backup/mobydick.epub", "mirror/mobydick.epub"} {
		if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
			Hash: "deadbeef",
			Path: p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "duplicates.json")
	if err := writeDuplicates(conn, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []duplicateReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 report, got %d", len(report))
	}
	r := report[0]
	if r.Path != "books/mobydick.epub" {
		t.Errorf("expected original path, got %q", r.Path)
	}
	if r.Title != "Moby Dick" {
		t.Errorf("expected title, got %q", r.Title)
	}
	if r.Authors != "Herman Melville" {
		t.Errorf("expected author, got %q", r.Authors)
	}
	if len(r.Duplicates) != 2 {
		t.Fatalf("expected 2 duplicate paths, got %v", r.Duplicates)
	}
	if r.Duplicates[0] != "backup/mobydick.epub" || r.Duplicates[1] != "mirror/mobydick.epub" {
		t.Errorf("unexpected duplicate paths %v", r.Duplicates)
	}
}
