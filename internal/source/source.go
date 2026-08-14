// Package source abstracts the locations margaret-tools reads ebooks from:
// the local filesystem or an FTP server. The scanner and the processor walk
// and open files through this interface so remote trees behave like local
// ones.
package source

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
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

// Local is the local-filesystem implementation of Source.
type Local struct{}

// List implements Source.
func (Local) List(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: e.Name(), IsDir: e.IsDir(), Size: info.Size()})
	}
	return out, nil
}

// Open implements Source.
func (Local) Open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// IsDir implements Source.
func (Local) IsDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// Join implements Source.
func (Local) Join(base, name string) string {
	return filepath.Join(base, name)
}

// Close implements Source.
func (Local) Close() error { return nil }

// FTPOptions configures the credentials used by FTP sources.
type FTPOptions struct {
	// User overrides the username. Empty falls back to the user info embedded
	// in the URL and then to the anonymous login.
	User string
	// Password overrides the password. Empty falls back to the user info
	// embedded in the URL and then to "anonymous@".
	Password string
}

// ftpSource is the FTP implementation of Source. It owns a single FTP
// connection and is therefore not safe for concurrent use.
type ftpSource struct {
	conn   *ftp.ServerConn
	prefix string // scheme://host, stripped from paths before talking to the server
}

// path strips the URL prefix so the remainder can be used in FTP commands.
func (s *ftpSource) path(p string) string {
	return strings.TrimPrefix(p, s.prefix)
}

// List implements Source.
func (s *ftpSource) List(path string) ([]Entry, error) {
	entries, err := s.conn.List(s.path(path))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		switch e.Type {
		case ftp.EntryTypeFolder:
			out = append(out, Entry{Name: e.Name, IsDir: true})
		default:
			out = append(out, Entry{Name: e.Name, Size: int64(e.Size)})
		}
	}
	return out, nil
}

// Open implements Source.
func (s *ftpSource) Open(path string) (io.ReadCloser, error) {
	return s.conn.Retr(s.path(path))
}

// IsDir implements Source.
func (s *ftpSource) IsDir(path string) (bool, error) {
	p := s.path(path)
	entries, err := s.conn.List(p)
	if err != nil {
		return false, err
	}
	// LIST of a directory returns its contents; LIST of a file returns the
	// file itself. A single entry whose name equals the requested path is
	// therefore the file, not a directory.
	if len(entries) == 1 {
		e := entries[0]
		if e.Name == filepath.Base(p) && e.Type != ftp.EntryTypeFolder {
			return false, nil
		}
	}
	return true, nil
}

// Join implements Source.
func (s *ftpSource) Join(base, name string) string {
	if strings.HasSuffix(base, "/") {
		return base + name
	}
	return base + "/" + name
}

// Close implements Source.
func (s *ftpSource) Close() error {
	return s.conn.Quit()
}

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
		return func() (Source, error) { return Local{}, nil }, nil
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
