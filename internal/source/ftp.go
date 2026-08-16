package source

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

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

// NewFTPSource dials an FTP server, logs in and returns a Source over the
// connection. The URL user info and opts supply the credentials, falling back
// to the anonymous login. It returns an error if the server cannot be reached
// or the login is rejected.
func NewFTPSource(u *url.URL, opts FTPOptions) (*ftpSource, error) {
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
	conn, err := ftp.Dial(host, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", u.Host, err)
	}
	if err := conn.Login(user, pass); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("login %s: %w", u.Host, err)
	}
	return &ftpSource{conn: conn, prefix: prefix}, nil
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
