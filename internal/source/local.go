package source

import (
	"io"
	"os"
	"path/filepath"
)

// LocalSource is the local-filesystem implementation of Source.
type LocalSource struct{}

// List implements Source.
func (l LocalSource) List(path string) ([]Entry, error) {
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
func (l LocalSource) Open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// IsDir implements Source.
func (l LocalSource) IsDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// Join implements Source.
func (l LocalSource) Join(base, name string) string {
	return filepath.Join(base, name)
}

// Close implements Source.
func (l LocalSource) Close() error {
	return nil
}
