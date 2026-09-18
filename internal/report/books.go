package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/f0d0r/margaret-cli/internal/db"
	"github.com/f0d0r/margaret-cli/internal/tx"
)

// BookFileReport is the JSON representation of one file belonging to a book.
type BookFileReport struct {
	Path    string   `json:"path"`
	Authors []string `json:"authors"`
	Title   string   `json:"title"`
}

// BookReport is the JSON representation of one book: the chosen
// (book-level) metadata plus every file grouped under it.
type BookReport struct {
	Authors []string         `json:"authors"`
	Title   string           `json:"title"`
	Files   []BookFileReport `json:"files"`
}

// Consolidate derives the canonical book-level metadata from the raw
// per-file metadata and persists it: books.title holds the consensus washed
// title (updating books_fts via triggers) and book_authors links to the
// picked, cleaned authors. book_files and book_file_authors are never
// touched, so the raw extracted metadata is preserved. Finally, authors
// referenced by neither book_authors nor book_file_authors are deleted
// (cleaning authors_fts via triggers). The pass is idempotent: it always
// recomputes from the raw file rows.
func Consolidate(ctx context.Context, sqldb *sql.DB, q *db.Queries) error {
	run := func(txCtx context.Context, txQ *db.Queries) error {
		reports, order, err := buildBookReports(txCtx, txQ)
		if err != nil {
			return err
		}
		for _, id := range order {
			r := reports[id]
			if err := txQ.UpdateBookTitle(txCtx, db.UpdateBookTitleParams{
				ID:    id,
				Title: r.Title,
			}); err != nil {
				return fmt.Errorf("update book %d title: %w", id, err)
			}
			if err := txQ.DeleteBookAuthorsByBookID(txCtx, id); err != nil {
				return fmt.Errorf("delete book %d authors: %w", id, err)
			}
			for _, name := range r.Authors {
				if err := txQ.CreateAuthor(txCtx, name); err != nil {
					return fmt.Errorf("create author %q: %w", name, err)
				}
				author, err := txQ.GetAuthorByName(txCtx, name)
				if err != nil {
					return fmt.Errorf("get author %q: %w", name, err)
				}
				if err := txQ.CreateBookAuthor(txCtx, db.CreateBookAuthorParams{
					BookID:   id,
					AuthorID: author.ID,
				}); err != nil {
					return fmt.Errorf("link book %d author %d: %w", id, author.ID, err)
				}
			}
		}
		if err := txQ.DeleteOrphanAuthors(txCtx); err != nil {
			return fmt.Errorf("delete orphan authors: %w", err)
		}
		return nil
	}

	if sqldb != nil {
		return tx.WithTx(ctx, sqldb, q, func(txCtx context.Context) error {
			return run(txCtx, tx.QueryFrom(txCtx, q))
		})
	}
	return run(ctx, q)
}

// WriteBooks writes a JSON report of the books found during the scan to path.
// Book-level title and authors are read from the consolidated books and
// book_authors tables (populated by Consolidate during scan); file entries
// keep their raw per-file metadata. Exact duplicates (same content hash) are
// listed under their parent book, inheriting the canonical file's metadata.
// When no books were found nothing is written, so no empty file is left
// behind.
func WriteBooks(ctx context.Context, q *db.Queries, path string) error {
	books, err := GetBooks(ctx, q)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write books report: %w", err)
	}
	return nil
}

// GetBooks returns the reports of all books in book ID order.
func GetBooks(ctx context.Context, q *db.Queries) ([]BookReport, error) {
	ids, err := q.ListBookIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list book ids: %w", err)
	}
	out := make([]BookReport, 0, len(ids))
	for _, id := range ids {
		r, ok, err := getBookReport(ctx, q, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetBooksFiltered returns the book reports for the given book IDs in the
// requested order. Unknown IDs are skipped. When ids is empty, nil is returned
// without reading the database.
//
// Book-level title and authors are read directly from the consolidated
// books/book_authors tables (populated by Consolidate during scan); only the
// requested books are loaded. File entries keep their raw per-file metadata.
func GetBooksFiltered(ctx context.Context, q *db.Queries, ids []int64) ([]BookReport, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[int64]bool, len(ids))
	var out []BookReport
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		r, ok, err := getBookReport(ctx, q, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// getBookReport reads one consolidated book report: the canonical title and
// authors plus the raw member files and exact duplicates. It reports false
// when the book ID is unknown.
func getBookReport(ctx context.Context, q *db.Queries, id int64) (BookReport, bool, error) {
	book, err := q.GetBookByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return BookReport{}, false, nil
	}
	if err != nil {
		return BookReport{}, false, fmt.Errorf("get book %d: %w", id, err)
	}
	authorRows, err := q.ListAuthorsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list authors of book %d: %w", id, err)
	}
	authors := make([]string, 0, len(authorRows))
	for _, a := range authorRows {
		authors = append(authors, a.Name)
	}
	fileRows, err := q.ListBookFilesWithAuthorsByBookID(ctx, id)
	if err != nil {
		return BookReport{}, false, fmt.Errorf("list files of book %d: %w", id, err)
	}
	files := make([]BookFileReport, 0, len(fileRows))
	byPath := make(map[string]bool, len(fileRows))
	for _, r := range fileRows {
		fileAuthors := filterFileAuthors(r.FileAuthors, r.FileTitle)
		files = append(files, BookFileReport{
			Path:    r.FilePath,
			Authors: fileAuthors,
			Title:   normalizeMeta(r.FileTitle),
		})
		byPath[r.FilePath] = true
	}
	if len(files) > 0 {
		dups, err := q.ListDuplicatesByBookID(ctx, id)
		if err != nil {
			return BookReport{}, false, fmt.Errorf("list duplicates of book %d: %w", id, err)
		}
		parent := files[0]
		for _, dupPath := range dups {
			if byPath[dupPath] {
				continue
			}
			files = append(files, BookFileReport{
				Path:    dupPath,
				Authors: parent.Authors,
				Title:   parent.Title,
			})
			byPath[dupPath] = true
		}
	}
	return BookReport{Authors: authors, Title: book.Title, Files: files}, true, nil
}

// filterFileAuthors splits a GROUP_CONCAT file-authors aggregate the same way
// buildBookReports does for file entries: placeholders are dropped and
// converter artifacts echoing the file title are filtered out.
func filterFileAuthors(aggregate, fileTitle string) []string {
	authors := splitAuthors(aggregate)
	fileTitle = normalizeMeta(fileTitle)
	kept := authors[:0]
	for _, name := range authors {
		if !isTitleEcho(name, fileTitle) {
			kept = append(kept, name)
		}
	}
	if kept == nil {
		return []string{}
	}
	return kept
}

// WriteBooksFiltered writes the same JSON shape as WriteBooks but only for
// the given book IDs and in the given order (e.g. FTS relevance order for
// search results). Unknown IDs are skipped. When the selection is empty
// nothing is written, so no empty file is left behind.
func WriteBooksFiltered(ctx context.Context, q *db.Queries, ids []int64, path string) error {
	if len(ids) == 0 {
		return nil
	}
	books, err := GetBooksFiltered(ctx, q, ids)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(books, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write books report: %w", err)
	}
	return nil
}

// buildBookReports runs the shared books-report pipeline: it groups member
// files under their books, attaches exact duplicates and resolves
// book-level authors and title with the pickAuthors/pickTitle heuristics.
// It returns the per-book reports keyed by book ID plus the natural
// (book ID) order.
func buildBookReports(ctx context.Context, q *db.Queries) (map[int64]BookReport, []int64, error) {
	rows, err := q.ListBooksWithFiles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list books with files: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	dups, err := q.ListBookFileDuplicatesWithBook(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list duplicates with book: %w", err)
	}

	type entry struct {
		titles    []string
		authorSet [][]string
		files     []BookFileReport
		byPath    map[string]bool
	}
	books := make(map[int64]*entry)
	var order []int64
	for _, r := range rows {
		b, ok := books[r.BookID]
		if !ok {
			b = &entry{byPath: make(map[string]bool)}
			books[r.BookID] = b
			order = append(order, r.BookID)
		}
		authors := splitAuthors(r.FileAuthors)
		fileTitle := normalizeMeta(r.FileTitle)
		// Drop converter artifacts where the file title was copied into
		// the author field (e.g. both read "Patkánykirály"): they carry
		// no author information and would block the Calibre post-pass.
		kept := authors[:0]
		for _, name := range authors {
			if !isTitleEcho(name, fileTitle) {
				kept = append(kept, name)
			}
		}
		authors = kept
		if authors == nil {
			authors = []string{}
		}
		b.titles = append(b.titles, r.FileTitle)
		b.authorSet = append(b.authorSet, authors)
		b.files = append(b.files, BookFileReport{
			Path:    r.FilePath,
			Authors: authors,
			Title:   fileTitle,
		})
		b.byPath[r.FilePath] = true
	}

	// Attach exact duplicates under their parent book. A duplicate carries
	// no metadata of its own, so it inherits the canonical file's title and
	// authors (same content hash). Orphan duplicates (no parent book, which
	// cannot happen in a scan but keeps the report robust) are skipped.
	for _, d := range dups {
		b := books[d.BookID]
		if b == nil || b.byPath[d.DuplicatePath] {
			continue
		}
		parent := b.files[0]
		b.files = append(b.files, BookFileReport{
			Path:    d.DuplicatePath,
			Authors: parent.Authors,
			Title:   parent.Title,
		})
		b.byPath[d.DuplicatePath] = true
	}

	reports := make(map[int64]BookReport, len(order))
	for _, id := range order {
		b := books[id]
		authors := pickAuthors(b.authorSet)
		// Union of the files' (placeholder-filtered) author names, so the
		// title heuristic can penalize "Author - Title" patterns.
		var authorNames []string
		seen := make(map[string]struct{})
		for _, list := range b.authorSet {
			for _, name := range list {
				if key := metaKey(name); key != "" {
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						authorNames = append(authorNames, name)
					}
				}
			}
		}
		title := pickTitle(b.titles, authorNames)
		needTitle := title == ""
		if needTitle && len(b.files) > 0 {
			// No file carried a usable title: fall back to the first
			// file's name. Book-level only; the file entries keep their
			// original titles.
			title = titleFromPath(b.files[0].Path)
		}
		calibreAuthor := ""
		if len(authors) == 0 {
			if needTitle {
				// Neither usable authors nor a usable title: a strict
				// Calibre-style "Title - Author" file name is the best
				// remaining source for both.
				for _, f := range b.files {
					if t, a, ok := parseCalibreFilename(f.Path); ok {
						title = t
						authors = []string{a}
						calibreAuthor = a
						break
					}
				}
			} else {
				// The title is known-good, only authors are missing: the
				// relaxed Calibre parse recovers a comma-less "First
				// Last" author without ever touching the title, so a
				// subtitle can never be corrupted here.
				for _, f := range b.files {
					if a, ok := parseCalibreAuthorOnly(f.Path); ok {
						authors = []string{a}
						calibreAuthor = a
						break
					}
				}
			}
		}
		if len(authors) > 0 {
			// Wash a credit affix ("Author - X", "X - Author",
			// "X by Author") off the picked title, whatever its source:
			// single-file (or noisy-majority) "Author - Title"
			// metadata has no clean rival to vote against it. The
			// guards inside washAuthorAffix keep genuine titles such
			// as "Stephen King Goes to the Movies" intact.
			washNames := authorNames
			if calibreAuthor != "" {
				washNames = append(washNames, calibreAuthor)
			}
			var sets [][]string
			for _, n := range washNames {
				sets = append(sets, matchTokens(n))
			}
			title = washAuthorAffix(title, sets)
		}
		reports[id] = BookReport{
			Authors: authors,
			Title:   title,
			Files:   b.files,
		}
	}
	return reports, order, nil
}
