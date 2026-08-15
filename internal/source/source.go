// Package source abstracts the locations margaret-tools reads ebooks from:
// the local filesystem or an FTP server. The scanner and the processor walk
// and open files through this interface so remote trees behave like local
// ones.
package source

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
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

// Factory creates a fresh Source; every call must return a usable instance.
type Factory func() (Source, error)

// FactoryForURL returns a Source Factory for rawURL. Plain local paths and
// "ftp://" URLs are supported; any other scheme is an error. FTP credentials
// come from opts, falling back to user info embedded in the URL and then to
// the anonymous login.
func FactoryForURL(rawURL string, opts FTPOptions) (Factory, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "":
		return func() (Source, error) { return LocalSource{}, nil }, nil
	case "ftp":
		return ftpFactory(u, opts)
	default:
		return nil, fmt.Errorf("unsupported scheme %q in %q (expected a path or an ftp:// URL)", u.Scheme, rawURL)
	}
}

func ftpFactory(u *url.URL, opts FTPOptions) (Factory, error) {
	user := opts.User
	pass := opts.Password
	if u.User != nil {
		if user == "" {
			user = u.User.Username()
		}
		if p, ok := u.User.Password(); ok && pass == "" {
			pass = p
		}
	}
	if user == "" {
		user = "anonymous"
	}
	if pass == "" {
		pass = "anonymous@"
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":21"
	}
	prefix := u.Scheme + "://" + u.Host
	return func() (Source, error) {
		conn, err := ftp.Dial(host, ftp.DialWithTimeout(30*time.Second))
		if err != nil {
			return nil, fmt.Errorf("connect %s: %w", u.Host, err)
		}
		if err := conn.Login(user, pass); err != nil {
			_ = conn.Quit()
			return nil, fmt.Errorf("login %s: %w", u.Host, err)
		}
		return &ftpSource{conn: conn, prefix: prefix}, nil
	}, nil
}
