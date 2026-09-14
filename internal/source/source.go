// Package source abstracts the locations margaret-cli reads ebooks from:
// the local filesystem or an FTP server. The scanner and the processor walk
// and open files through this interface so remote trees behave like local
// ones.
package source

import (
	"fmt"
	"io"
	"net/url"
)

// Entry describes one item of a directory listing.
type Entry struct {
	Name  string
	IsDir bool
	Size  int64
}

// Source abstracts a tree of files. A Source is not safe for concurrent use;
// the scanner and the processor create one per worker goroutine.
type Source interface {
	// List returns the entries of the directory at path.
	List(path string) ([]Entry, error)

	// Open returns a reader over the file at path. Local files yield an
	// *os.File; remote sources yield a network stream.
	Open(path string) (io.ReadCloser, error)

	// IsDir reports whether path is a directory.
	IsDir(path string) (bool, error)

	// Join appends name to base using the source's path separator.
	Join(base, name string) string

	// Close releases the underlying resources.
	Close() error
}

// SourceFactory returns a Source for rawURL. Plain local paths and "ftp://"
// URLs are supported; any other scheme is an error. FTP credentials come from
// opts, falling back to user info embedded in the URL and then to the
// anonymous login. The returned Source is not safe for concurrent use; call
// the factory once per worker goroutine.
func SourceFactory(rawURL string, opts FTPOptions) (Source, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "":
		return LocalSource{}, nil
	case "ftp":
		return NewFTPSource(u, opts)
	default:
		return nil, fmt.Errorf("unsupported scheme %q in %q (expected a path or an ftp:// URL)", u.Scheme, rawURL)
	}
}
