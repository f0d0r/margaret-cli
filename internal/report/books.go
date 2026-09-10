package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/f0d0r/margaret-tools/internal/db"
)

// bookFileReport is the JSON representation of one file belonging to a book.
type bookFileReport struct {
	Path    string   `json:"path"`
	Authors []string `json:"authors"`
	Title   string   `json:"title"`
}

// bookReport is the JSON representation of one book: the chosen
// (book-level) metadata plus every file grouped under it.
type bookReport struct {
	Authors []string         `json:"authors"`
	Title   string           `json:"title"`
	Files   []bookFileReport `json:"files"`
}

// WriteBooks writes a JSON report of the books found during the scan to path.
// Book-level authors and title are picked from the member files with the
// pickAuthors/pickTitle heuristics; exact duplicates (same content hash) are
// listed under their parent book, inheriting the canonical file's metadata.
// When no books were found nothing is written, so no empty file is left
// behind.
func WriteBooks(q *db.Queries, path string) error {
	ctx := context.Background()

	rows, err := q.ListBooksWithFiles(ctx)
	if err != nil {
		return fmt.Errorf("list books with files: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	dups, err := q.ListBookFileDuplicatesWithBook(ctx)
	if err != nil {
		return fmt.Errorf("list duplicates with book: %w", err)
	}

	type entry struct {
		titles    []string
		authorSet [][]string
		files     []bookFileReport
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
		b.files = append(b.files, bookFileReport{
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
		b, ok := books[d.BookID]
		if !ok || b.byPath[d.DuplicatePath] {
			continue
		}
		parent := b.files[0]
		b.files = append(b.files, bookFileReport{
			Path:    d.DuplicatePath,
			Authors: parent.Authors,
			Title:   parent.Title,
		})
		b.byPath[d.DuplicatePath] = true
	}

	report := make([]bookReport, 0, len(order))
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
		report = append(report, bookReport{
			Authors: authors,
			Title:   title,
			Files:   b.files,
		})
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write books report: %w", err)
	}
	return nil
}
