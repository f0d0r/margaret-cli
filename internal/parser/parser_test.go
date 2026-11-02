package parser

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
	ebook "github.com/f0d0r/margaret-ebook-library"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
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
    <dc:description>A test description.</dc:description>
    <dc:subject>Fiction</dc:subject>
    <dc:subject>Adventure</dc:subject>
    <dc:language>en</dc:language>
    <dc:identifier scheme="ISBN">9780000000000</dc:identifier>
    <dc:publisher>Test Press</dc:publisher>
    <dc:date>2020-01-02</dc:date>
    <meta name="calibre:series" content="Test Series"/>
    <meta name="calibre:series_index" content="2"/>
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

const opfWithoutISBN = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Text ISBN Book</dc:title>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2" linear="no"/>
  </spine>
</package>`

const opfInvalidISBN = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Text ISBN Book</dc:title>
    <dc:language>en</dc:language>
    <dc:identifier scheme="ISBN">9780000000000</dc:identifier>
  </metadata>
  <manifest>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
  </spine>
</package>`

const opfManifestOnly = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Text ISBN Book</dc:title>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
  </spine>
</package>`

const opfWithValidISBN = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Text ISBN Book</dc:title>
    <dc:language>en</dc:language>
    <dc:identifier scheme="ISBN">9780306406157</dc:identifier>
  </metadata>
  <manifest>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
  </spine>
</package>`

type epubPage struct {
	name    string
	content string
}

func xhtmlPage(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>` + body + `</body></html>`
}

func writeEPUBWithPages(t *testing.T, path, opf string, pages []epubPage) {
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
	files := []epubPage{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", containerXML},
		{"OEBPS/content.opf", opf},
	}
	files = append(files, pages...)
	for _, m := range files {
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

func TestEbookParserExtractsISBNFromNonLinearText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithoutISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>No identifier on this page.</p>")},
		{"OEBPS/c2.xhtml", xhtmlPage("<p>Copyright ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if want := []identifier.Identifier{{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceText}}; !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestEbookParserExtractsISBNFromManifestOnlyText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfManifestOnly, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>No identifier on this page.</p>")},
		{"OEBPS/c2.xhtml", xhtmlPage("<p>ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if want := []identifier.Identifier{{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceText}}; !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestEbookParserIgnoresISBNAfterNonLinearHeadWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithoutISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>No identifier on this page.</p>")},
		{"OEBPS/c2.xhtml", xhtmlPage("<p>" + strings.Repeat("filler ", 10_000) + "ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Identifiers) != 0 {
		t.Errorf("identifiers = %v, want none", md.Identifiers)
	}
}

func TestEbookParserIgnoresISBNAfterHeadWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithoutISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>" + strings.Repeat("filler ", 10_000) + "ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Identifiers) != 0 {
		t.Errorf("identifiers = %v, want none", md.Identifiers)
	}
}

func TestEbookParserMarksMetadataAndTextWhenBothMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithValidISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if want := []identifier.Identifier{{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadataAndText}}; !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestEbookParserAlwaysScansText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithValidISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>ISBN 9780375704024</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	want := []identifier.Identifier{
		{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9780375704024", Source: identifier.SourceText},
	}
	if !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestEbookParserNormalizesArabicIndicTextISBN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithoutISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>ISBN ٩٧٨٠٣٠٦٤٠٦١٥٧</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if want := []identifier.Identifier{{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceText}}; !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestEbookParserScansTextWhenMetadataISBNIsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfInvalidISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>ISBN 978-0-306-40615-7</p>")},
	})

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	want := []identifier.Identifier{
		{Scheme: "ISBN", Value: "9780000000000", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceText},
	}
	if !slices.Equal(md.Identifiers, want) {
		t.Errorf("identifiers = %v, want %v", md.Identifiers, want)
	}
}

func TestFingerprintBookMatchesFingerprintContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUBWithPages(t, path, opfWithoutISBN, []epubPage{
		{"OEBPS/c1.xhtml", xhtmlPage("<p>" + "A long enough chapter for a stable fingerprint. " + "</p>")},
		{"OEBPS/c2.xhtml", xhtmlPage("<p>Non-linear notes.</p>")},
	})

	bk, err := ebook.ReadFromBlob(book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	want, err := tools.FingerprintContent(context.Background(), bk)
	if err != nil {
		t.Fatal(err)
	}
	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(md.MinHash, want.MinHash) {
		t.Errorf("MinHash changed: got %v, want %v", md.MinHash, want.MinHash)
	}
}

func TestEbookParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUB(t, path)

	md, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(path))
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "Test Book" {
		t.Errorf("expected title %q, got %q", "Test Book", md.Title)
	}
	if want := []string{"Jane Doe", "John Smith"}; !slices.Equal(md.Authors, want) {
		t.Errorf("expected authors %v, got %v", want, md.Authors)
	}
	if md.Description != "A test description." {
		t.Errorf("expected description %q, got %q", "A test description.", md.Description)
	}
	if want := []string{"Fiction", "Adventure"}; !slices.Equal(md.Subjects, want) {
		t.Errorf("expected subjects %v, got %v", want, md.Subjects)
	}
	if want := []string{"en"}; !slices.Equal(md.Languages, want) {
		t.Errorf("expected languages %v, got %v", want, md.Languages)
	}
	if want := []identifier.Identifier{{Scheme: "ISBN", Value: "9780000000000", Source: identifier.SourceMetadata}}; !slices.Equal(md.Identifiers, want) {
		t.Errorf("expected identifiers %v, got %v", want, md.Identifiers)
	}
	if md.Publisher != "Test Press" {
		t.Errorf("expected publisher %q, got %q", "Test Press", md.Publisher)
	}
	if md.PublicationDate != "2020-01-02" {
		t.Errorf("expected publication date %q, got %q", "2020-01-02", md.PublicationDate)
	}
	if md.Series != "Test Series" {
		t.Errorf("expected series %q, got %q", "Test Series", md.Series)
	}
	if md.SeriesIndex != "2" {
		t.Errorf("expected series index %q, got %q", "2", md.SeriesIndex)
	}
}

func TestEbookParserMissingFile(t *testing.T) {
	_, err := EbookParser{}.Parse(context.Background(), book.NewPathBlob(filepath.Join(t.TempDir(), "missing.epub")))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestEbookParserCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	writeEPUB(t, path)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := EbookParser{}.Parse(ctx, book.NewPathBlob(path))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestContextBlobCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := withContext(ctx, book.NewBytesBlob([]byte("data")))
	if _, err := b.Size(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Size: expected context.Canceled, got %v", err)
	}
	if _, err := b.ReadAt(make([]byte, 4), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadAt: expected context.Canceled, got %v", err)
	}
}
