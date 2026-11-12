// Package tempfile creates temporary files that are unlinked immediately, so
// they disappear when they are closed or when the process exits — including an
// abrupt SIGKILL. The file is always created in the system temporary
// directory.
package tempfile

import (
	"fmt"
	"io"
	"os"
)

// Temp is an unlinked temporary file. It cannot be reopened by name; readers
// and writers must use the retained descriptor.
type Temp struct {
	file *os.File
	name string
}

// New creates an unlinked temporary file in the system temporary directory.
// The returned Temp must be closed when it is no longer needed.
func New(pattern string) (*Temp, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, fmt.Errorf("create temporary file: %w", err)
	}
	name := file.Name()
	if err := os.Remove(name); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("unlink temporary file: %w", err)
	}
	return &Temp{file: file, name: name}, nil
}

// File returns the open file descriptor.
func (t *Temp) File() *os.File {
	return t.file
}

// Name returns the path the file had before it was unlinked. It no longer
// exists on disk; the value is useful for diagnostics and tests.
func (t *Temp) Name() string {
	return t.name
}

// Reader returns a reader over the current contents without changing the file
// offset. The caller must not write to the file while reading.
func (t *Temp) Reader() (io.Reader, error) {
	info, err := t.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat temporary file: %w", err)
	}
	return io.NewSectionReader(t.file, 0, info.Size()), nil
}

// Close releases the file descriptor. Because the file was unlinked, closing
// it also frees the disk space.
func (t *Temp) Close() error {
	if t == nil || t.file == nil {
		return nil
	}
	err := t.file.Close()
	t.file = nil
	return err
}
