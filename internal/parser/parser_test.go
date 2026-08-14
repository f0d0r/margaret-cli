package parser

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/f0d0r/margaret-ebook-library/pkg/model"
)

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const opfXML = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
    <dc:creator>Jane Doe</dc:creator>
    <dc:creator>John Smith</dc:creator>
  </metadata>
</package>`

// writeEPUB writes a minimal but valid EPUB to path.
func writeEPUB(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	zw := zip.NewWriter(f)
	for _, m := range []struct{ name, content string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", containerXML},
		{"OEBPS/content.opf", opfXML},
	} {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(m.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEbookParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUB(t, path)

	md, err := EbookParser{}.Parse(model.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Test Book" {
		t.Errorf("expected title %q, got %q", "Test Book", md.Title)
	}
	wantAuthors := []string{"Jane Doe", "John Smith"}
	if len(md.Authors) != len(wantAuthors) {
		t.Fatalf("expected authors %v, got %v", wantAuthors, md.Authors)
	}
	for i, want := range wantAuthors {
		if md.Authors[i] != want {
			t.Errorf("expected author %q, got %q", want, md.Authors[i])
		}
	}
}

func TestEbookParserMissingFile(t *testing.T) {
	_, err := EbookParser{}.Parse(model.NewPathBlob(filepath.Join(t.TempDir(), "missing.epub")))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
