package source

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-tools/internal/ftptest"
)

func TestFactoryForURL(t *testing.T) {
	f, err := FactoryForURL("/tmp/books", FTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := f()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.(Local); !ok {
		t.Fatalf("expected Local source for a plain path, got %T", src)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := FactoryForURL("http://example.com/books", FTPOptions{}); err == nil {
		t.Fatal("expected error for an unsupported scheme")
	}
}

func TestFTPSource(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"a.epub":       "epub-content",
		"sub/b.mobi":   "mobi-content",
		"nested/x.txt": "not-an-ebook",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := ftptest.NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	f, err := FactoryForURL("ftp://"+srv.Addr(), FTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := f()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = src.Close()
	}()

	if isDir, err := src.IsDir("ftp://" + srv.Addr()); err != nil || !isDir {
		t.Fatalf("IsDir(root) = %v, %v; want true, nil", isDir, err)
	}

	entries, err := src.List("ftp://" + srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries at root, got %d: %+v", len(entries), entries)
	}

	dirs, filesCount := 0, 0
	var epubSize int64
	for _, e := range entries {
		if e.IsDir {
			dirs++
			if e.Name != "sub" && e.Name != "nested" {
				t.Errorf("unexpected directory %q", e.Name)
			}
			continue
		}
		filesCount++
		switch e.Name {
		case "a.epub":
			epubSize = e.Size
		case "nested/x.txt":
		}
	}
	if dirs != 2 || filesCount != 1 {
		t.Fatalf("dirs=%d files=%d, want dirs=2 files=1", dirs, filesCount)
	}
	if epubSize != int64(len("epub-content")) {
		t.Fatalf("a.epub size = %d, want %d", epubSize, len("epub-content"))
	}

	rc, err := src.Open("ftp://" + srv.Addr() + "/a.epub")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rc.Close()
	}()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "epub-content" {
		t.Fatalf("read %q, want %q", got, "epub-content")
	}

	// The URL user info is passed through as the login credentials.
	if _, err := FactoryForURL("ftp://user:pass@"+srv.Addr(), FTPOptions{}); err != nil {
		t.Fatal(err)
	}

	// The --ftp-user/--ftp-pass options override the URL.
	if _, err := FactoryForURL("ftp://"+srv.Addr(), FTPOptions{User: "x", Password: "y"}); err != nil {
		t.Fatal(err)
	}
}
