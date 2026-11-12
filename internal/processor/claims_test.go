package processor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/claim"
	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-ebook-library/book"
)

type claimsParser struct{}

func (claimsParser) Parse(context.Context, book.Blob) (parser.Metadata, error) {
	return parser.Metadata{
		Title:   "Legacy Title",
		Authors: []string{"Legacy Author"},
		Head:    "Title: Moby Dick\nAuthor: Herman Melville\n\nCall me Ishmael.",
		Claims: []claim.Set{
			{
				Key: "metadata", Source: claim.SourceMetadata, Origin: "test",
				Title: "Moby Dick", Authors: []string{"Herman Melville"},
				Trust: claim.TrustStrong, Witness: claim.WitnessIndependent,
			},
			{
				Key: "text:label", Source: claim.SourceText, Origin: "text:label",
				Title: "Moby Dick", Authors: []string{"Herman Melville"},
				Trust: claim.TrustStrong, Witness: claim.WitnessIndependent,
			},
		},
		Hash: "claims-hash",
	}, nil
}

func TestResumeBackfillsClaimsWithoutOverwritingLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	q := db.New(conn)
	if _, err := q.CreateBookFile(context.Background(), db.CreateBookFileParams{
		Hash:        "claims-hash",
		Path:        path,
		Title:       "Old Title",
		LegacyTitle: "Old Title",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultScanProcessorConfig()
	cfg.Resume = true
	proc := NewScanProcessor(conn, q, cfg, dir)
	proc.parsers = map[string]parser.Parser{"epub": claimsParser{}}
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	var title, legacyTitle, titleStatus string
	var claimVersion, resolveVersion int64
	if err := conn.QueryRow(
		`SELECT title, legacy_title, title_status, claim_version, resolve_version
		 FROM book_files WHERE path = ?`, path,
	).Scan(&title, &legacyTitle, &titleStatus, &claimVersion, &resolveVersion); err != nil {
		t.Fatal(err)
	}
	if title != "Moby Dick" || legacyTitle != "Old Title" {
		t.Fatalf("title = %q, legacy = %q", title, legacyTitle)
	}
	if titleStatus != "corroborated" {
		t.Fatalf("title status = %q", titleStatus)
	}
	if claimVersion != claim.ClaimVersion || resolveVersion != claim.ResolveVersion {
		t.Fatalf("versions = %d/%d", claimVersion, resolveVersion)
	}
	var claims int
	if err := conn.QueryRow(`SELECT count(*) FROM book_file_claims`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims == 0 {
		t.Fatal("expected backfilled claims")
	}
}

func TestResolveStaleClaimsReusesStoredClaims(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	q := db.New(conn)
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:           "stale-hash",
		Path:           "book.epub",
		Title:          "Stale Title",
		LegacyTitle:    "Legacy Title",
		ClaimVersion:   claim.ClaimVersion,
		ResolveVersion: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []db.CreateBookFileClaimParams{
		{
			BookFileID: fileID, SetKey: "metadata", Source: "METADATA", Origin: "test",
			Field: "title", Value: "Moby Dick", Trust: "STRONG", Witness: "INDEPENDENT",
			AttestationMethod: "none",
		},
		{
			BookFileID: fileID, SetKey: "metadata", Source: "METADATA", Origin: "test",
			Field: "author", Value: "Herman Melville", Trust: "STRONG", Witness: "INDEPENDENT",
			AttestationMethod: "none",
		},
	} {
		if err := q.CreateBookFileClaim(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	if err := ResolveStaleClaims(ctx, conn, q); err != nil {
		t.Fatal(err)
	}

	var title, legacyTitle, titleStatus string
	var resolveVersion int64
	if err := conn.QueryRow(
		`SELECT title, legacy_title, title_status, resolve_version FROM book_files WHERE id = ?`, fileID,
	).Scan(&title, &legacyTitle, &titleStatus, &resolveVersion); err != nil {
		t.Fatal(err)
	}
	if title != "Moby Dick" || legacyTitle != "Legacy Title" {
		t.Fatalf("title = %q, legacy = %q", title, legacyTitle)
	}
	if titleStatus != "single" {
		t.Fatalf("title status = %q", titleStatus)
	}
	if resolveVersion != claim.ResolveVersion {
		t.Fatalf("resolve version = %d", resolveVersion)
	}
}

func TestProcessStoresTitleAuthorClaims(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Herman_Melville_-_Moby-Dick.epub")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	proc := NewScanProcessor(conn, db.New(conn), DefaultScanProcessorConfig(), dir)
	proc.parsers = map[string]parser.Parser{"epub": claimsParser{}}
	if err := proc.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures := proc.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	var (
		title, legacyTitle, titleStatus, authorStatus, titleSet, authorSet string
		claimVersion, resolveVersion                                       int64
	)
	if err := conn.QueryRow(
		`SELECT title, legacy_title, title_status, author_status, title_set,
		        author_set, claim_version, resolve_version
		 FROM book_files WHERE path = ?`, path,
	).Scan(&title, &legacyTitle, &titleStatus, &authorStatus, &titleSet, &authorSet, &claimVersion, &resolveVersion); err != nil {
		t.Fatal(err)
	}
	if title != "Moby Dick" || legacyTitle != "Legacy Title" {
		t.Fatalf("title = %q, legacy = %q", title, legacyTitle)
	}
	if titleStatus != "corroborated" || authorStatus != "corroborated" {
		t.Fatalf("statuses = %q/%q", titleStatus, authorStatus)
	}
	if titleSet != "metadata" || authorSet != "metadata" {
		t.Fatalf("sets = %q/%q", titleSet, authorSet)
	}
	if claimVersion != claim.ClaimVersion || resolveVersion != claim.ResolveVersion {
		t.Fatalf("versions = %d/%d", claimVersion, resolveVersion)
	}

	var claimCount int
	if err := conn.QueryRow(`SELECT count(*) FROM book_file_claims`).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if claimCount == 0 {
		t.Fatal("expected stored claims")
	}

	var legacyAuthor string
	if err := conn.QueryRow(`SELECT name FROM book_file_legacy_authors LIMIT 1`).Scan(&legacyAuthor); err != nil {
		t.Fatal(err)
	}
	if legacyAuthor != "Legacy Author" {
		t.Fatalf("legacy author = %q", legacyAuthor)
	}
}
