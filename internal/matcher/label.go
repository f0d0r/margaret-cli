package matcher

import "strings"

// bookLabel formats a scanned book for progress output.
func bookLabel(book *Book) string {
	if book == nil {
		return ""
	}
	title := strings.TrimSpace(book.Title)
	if title == "" {
		title = "(untitled)"
	}
	for _, author := range book.Authors {
		name := strings.TrimSpace(author)
		if name == "" {
			continue
		}
		if comma := strings.IndexByte(name, ','); comma > 0 {
			name = strings.TrimSpace(name[:comma])
		}
		return name + ": " + title
	}
	return title
}
