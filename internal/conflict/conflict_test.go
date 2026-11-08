package conflict

import (
	"context"
	"database/sql"
	"encoding/binary"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

func openConflictDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn, db.New(conn)
}

func signature(offset uint64) []byte {
	values := make([]uint64, 512)
	for i := range values {
		values[i] = uint64(i) + offset
	}
	blob := make([]byte, len(values)*8)
	for i, value := range values {
		binary.LittleEndian.PutUint64(blob[i*8:], value)
	}
	return blob
}

func seedBook(t *testing.T, q *db.Queries, title, author, hash string, minhash []byte) int64 {
	t.Helper()
	ctx := context.Background()
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:    hash,
		Path:    "books/" + hash + ".epub",
		Title:   title,
		Minhash: minhash,
	})
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := q.CreateBook(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if author != "" {
		if err := q.CreateAuthor(ctx, author); err != nil {
			t.Fatal(err)
		}
		record, err := q.GetAuthorByName(ctx, author)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookAuthor(ctx, db.CreateBookAuthorParams{
			BookID:   bookID,
			AuthorID: record.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return bookID
}

func seedMatch(t *testing.T, q *db.Queries, bookID int64, status string) {
	t.Helper()
	ctx := context.Background()
	if err := q.UpsertBookMatch(ctx, db.UpsertBookMatchParams{
		BookID:     bookID,
		Status:     status,
		Confidence: 1,
		Method:     "isbn",
		MatchedAt:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetBookMatchStatus(ctx, db.SetBookMatchStatusParams{
		MatchStatus: status,
		ID:          bookID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDetectConflicts(t *testing.T) {
	_, q := openConflictDB(t)

	conflictA := seedBook(t, q, "Conflict", "Author", "conflict-a", signature(0))
	conflictB := seedBook(t, q, "Conflict", "Author", "conflict-b", signature(10000))

	sameA := seedBook(t, q, "Same", "Author", "same-a", signature(0))
	sameB := seedBook(t, q, "Same", "Author", "same-b", signature(0))

	seedBook(t, q, "Different Authors", "Author One", "authors-a", signature(0))
	seedBook(t, q, "Different Authors", "Author Two", "authors-b", signature(10000))

	seedBook(t, q, "Mixed Authors", "Author One", "mixed-a", signature(0))
	seedBook(t, q, "Mixed Authors", "", "mixed-b", signature(10000))

	titleA := seedBook(t, q, "Title Only", "", "title-a", signature(0))
	titleB := seedBook(t, q, "Title Only", "", "title-b", signature(10000))

	seedBook(t, q, "No MinHash", "Author", "nohash-a", nil)
	seedBook(t, q, "No MinHash", "Author", "nohash-b", signature(10000))

	result, err := Detect(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Conflicts != 2 {
		t.Fatalf("conflicts = %d, want 2", result.Stats.Conflicts)
	}
	if !hasConflict(result, conflictA, conflictB) || !hasConflict(result, conflictB, conflictA) {
		t.Fatalf("missing Conflict pair: %+v", result.Pairs)
	}
	if !hasConflict(result, titleA, titleB) || !hasConflict(result, titleB, titleA) {
		t.Fatalf("missing title-only pair: %+v", result.Pairs)
	}
	if hasConflict(result, sameA, sameB) {
		t.Fatal("similar content must not conflict")
	}
}

func TestDetectAndPersistResetsStatuses(t *testing.T) {
	conn, q := openConflictDB(t)
	first := seedBook(t, q, "Conflict", "Author", "persist-a", signature(0))
	second := seedBook(t, q, "Conflict", "Author", "persist-b", signature(10000))
	seedMatch(t, q, first, "confirmed")
	seedMatch(t, q, second, "confirmed")

	result, err := DetectAndPersist(context.Background(), conn, q, Options{Now: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", result.Stats.Conflicts)
	}

	for _, bookID := range []int64{first, second} {
		var finalStatus, matchStatus string
		if err := conn.QueryRow("SELECT match_status FROM books WHERE id = ?", bookID).Scan(&finalStatus); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow("SELECT status FROM book_matches WHERE book_id = ?", bookID).Scan(&matchStatus); err != nil {
			t.Fatal(err)
		}
		if finalStatus != "needs_review" || matchStatus != "confirmed" {
			t.Fatalf("book %d statuses = %q / %q", bookID, finalStatus, matchStatus)
		}
	}

	var count int
	if err := conn.QueryRow("SELECT count(*) FROM book_conflicts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("conflict rows = %d, want 2", count)
	}

	// Re-running is idempotent.
	if _, err := DetectAndPersist(context.Background(), conn, q, Options{Now: time.Unix(2, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT count(*) FROM book_conflicts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("conflict rows after rerun = %d, want 2", count)
	}
}

func TestDetectAndPersistClearsResolvedConflict(t *testing.T) {
	conn, q := openConflictDB(t)
	first := seedBook(t, q, "Conflict", "Author", "resolved-a", signature(0))
	second := seedBook(t, q, "Conflict", "Author", "resolved-b", signature(10000))
	seedMatch(t, q, first, "confirmed")
	seedMatch(t, q, second, "confirmed")

	if _, err := DetectAndPersist(context.Background(), conn, q, Options{}); err != nil {
		t.Fatal(err)
	}

	// Make the second book's content identical to the first.
	if _, err := conn.Exec("UPDATE book_files SET minhash = ? WHERE path = ?",
		signature(0), "books/resolved-b.epub"); err != nil {
		t.Fatal(err)
	}
	result, err := DetectAndPersist(context.Background(), conn, q, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Conflicts != 0 {
		t.Fatalf("conflicts = %d, want 0", result.Stats.Conflicts)
	}
	var count int
	if err := conn.QueryRow("SELECT count(*) FROM book_conflicts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("conflict rows = %d, want 0", count)
	}
	for _, bookID := range []int64{first, second} {
		var status string
		if err := conn.QueryRow("SELECT match_status FROM books WHERE id = ?", bookID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "confirmed" {
			t.Fatalf("book %d final status = %q, want confirmed", bookID, status)
		}
	}
}

func hasConflict(result *Result, bookID, peerID int64) bool {
	for _, peer := range result.Books[bookID] {
		if peer.PeerBookID == peerID {
			return true
		}
	}
	return false
}
