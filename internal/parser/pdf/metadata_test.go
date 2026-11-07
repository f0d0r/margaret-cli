package pdf

import (
	"slices"
	"strings"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/identifier"
)

func TestParseInfo(t *testing.T) {
	raw := "Title: The PDF Book\n" +
		"Author: Jane Doe & John Smith\n" +
		"Subject: A description\n" +
		"Keywords: Fiction, Adventure, 978-3-16-148410-0\n" +
		"ModDate: 2021-02-03T04:05:06+01\n" +
		"Metadata Stream: yes\n" +
		"Encrypted: no\n"
	info := parseInfo(raw)
	if got := info.get("Title"); got != "The PDF Book" {
		t.Errorf("title = %q", got)
	}
	if got := info.get("Metadata Stream"); got != "yes" {
		t.Errorf("metadata stream = %q", got)
	}
	md := metadataFromInfo(info)
	if md.title != "The PDF Book" {
		t.Errorf("title = %q", md.title)
	}
	if !slices.Equal(md.authors, []string{"Jane Doe", "John Smith"}) {
		t.Errorf("authors = %v", md.authors)
	}
	if md.description != "A description" {
		t.Errorf("description = %q", md.description)
	}
	if !slices.Equal(md.subjects, []string{"Fiction", "Adventure"}) {
		t.Errorf("subjects = %v", md.subjects)
	}
	if md.modDate.IsZero() {
		t.Error("expected a parsed ModDate")
	}
	wantID := identifier.Identifier{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceMetadata}
	if !slices.Contains(md.identifiers, wantID) {
		t.Errorf("identifiers = %v, want %v", md.identifiers, wantID)
	}
}

func TestParseXMP(t *testing.T) {
	raw := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
  xmlns:dc="http://purl.org/dc/elements/1.1/"
  xmlns:pdf="http://ns.adobe.com/pdf/1.3/"
  xmlns:xmp="http://ns.adobe.com/xap/1.0/"
  xmlns:calibre="http://calibre-ebook.com/xmp-namespace"
  xmlns:calibreSI="http://calibre-ebook.com/xmp-namespace-series-index"
  xmlns:prism="http://prismstandard.org/namespaces/basic/2.0/">
  <rdf:Description rdf:about="">
   <dc:title><rdf:Alt><rdf:li xml:lang="x-default">XMP Title</rdf:li></rdf:Alt></dc:title>
   <dc:creator><rdf:Seq><rdf:li>Alice</rdf:li><rdf:li>Bob</rdf:li></rdf:Seq></dc:creator>
   <dc:description><rdf:Alt><rdf:li xml:lang="x-default">XMP Description</rdf:li></rdf:Alt></dc:description>
   <dc:subject><rdf:Bag><rdf:li>Fiction</rdf:li><rdf:li>Adventure</rdf:li></rdf:Bag></dc:subject>
   <dc:publisher><rdf:Bag><rdf:li>Acme Press</rdf:li></rdf:Bag></dc:publisher>
   <dc:date><rdf:Seq><rdf:li>2020-01-02</rdf:li></rdf:Seq></dc:date>
   <dc:language><rdf:Bag><rdf:li>en</rdf:li></rdf:Bag></dc:language>
   <xmp:MetadataDate>2021-01-01T00:00:00Z</xmp:MetadataDate>
   <calibre:series rdf:parseType="Resource">
    <rdf:value>Series Name</rdf:value>
    <calibreSI:series_index>2</calibreSI:series_index>
   </calibre:series>
   <prism:isbn><rdf:Seq><rdf:li>978-3-16-148410-0</rdf:li></rdf:Seq></prism:isbn>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

	md, err := parseXMP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if md.title != "XMP Title" {
		t.Errorf("title = %q, want %q", md.title, "XMP Title")
	}
	if !slices.Equal(md.authors, []string{"Alice", "Bob"}) {
		t.Errorf("authors = %v", md.authors)
	}
	if md.description != "XMP Description" {
		t.Errorf("description = %q", md.description)
	}
	if !slices.Equal(md.subjects, []string{"Fiction", "Adventure"}) {
		t.Errorf("subjects = %v", md.subjects)
	}
	if md.publisher != "Acme Press" {
		t.Errorf("publisher = %q", md.publisher)
	}
	if md.publicationDate != "2020-01-02" {
		t.Errorf("publication date = %q", md.publicationDate)
	}
	if !slices.Equal(md.languages, []string{"en"}) {
		t.Errorf("languages = %v", md.languages)
	}
	if md.series != "Series Name" || md.seriesIndex != "2" {
		t.Errorf("series = %q/%q", md.series, md.seriesIndex)
	}
	if md.xmpDate.IsZero() {
		t.Error("expected a parsed XMP metadata date")
	}
	wantID := identifier.Identifier{Scheme: "ISBN", Value: "9783161484100", Source: identifier.SourceMetadata}
	if !slices.Contains(md.identifiers, wantID) {
		t.Errorf("identifiers = %v, want %v", md.identifiers, wantID)
	}
}

func TestParseXMPKeywordsFallback(t *testing.T) {
	raw := `<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
  xmlns:pdf="http://ns.adobe.com/pdf/1.3/">
  <rdf:Description rdf:about="">
   <pdf:Keywords>Fiction, Adventure</pdf:Keywords>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>`
	md, err := parseXMP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(md.subjects, []string{"Fiction", "Adventure"}) {
		t.Errorf("subjects = %v, want Fiction and Adventure", md.subjects)
	}
}

func TestMergeMetadata(t *testing.T) {
	info := metadata{title: "Info Title", authors: []string{"Info Author"}, description: "Info Description"}
	xmp := metadata{title: "XMP Title", authors: []string{"XMP Author"}, description: "XMP Description"}

	merged := mergeMetadata(info, xmp)
	if merged.title != "XMP Title" {
		t.Errorf("title = %q, want XMP title", merged.title)
	}
	if !slices.Equal(merged.authors, []string{"XMP Author"}) {
		t.Errorf("authors = %v, want XMP authors", merged.authors)
	}

	info.modDate = parsePDFDate("2022-01-01T00:00:00Z")
	xmp.xmpDate = parsePDFDate("2021-01-01T00:00:00Z")
	merged = mergeMetadata(info, xmp)
	if merged.title != "Info Title" {
		t.Errorf("title = %q, want Info title when Info ModDate is newer", merged.title)
	}
	if !slices.Equal(merged.authors, []string{"Info Author"}) {
		t.Errorf("authors = %v, want Info authors when Info ModDate is newer", merged.authors)
	}
}

func TestIdentifiersFromTexts(t *testing.T) {
	ids := identifiersFromTexts([]string{
		"ISBN 978-3-16-148410-0 and doi:10.1234/Example",
		"978-3-16-148410-0",
	})
	if len(ids) != 2 {
		t.Fatalf("identifiers = %v, want an ISBN and a DOI", ids)
	}
	if ids[0].Scheme != "ISBN" || ids[0].Value != "9783161484100" {
		t.Errorf("first identifier = %+v", ids[0])
	}
	if ids[1].Scheme != "DOI" || ids[1].Value != "10.1234/example" {
		t.Errorf("second identifier = %+v", ids[1])
	}
}

func TestIdentifiersFromTextsDropsPlaceholderISBN(t *testing.T) {
	ids := identifiersFromTexts([]string{
		"ISBN 978-0-000000-00-2 and doi:10.1234/Example",
	})
	if len(ids) != 1 {
		t.Fatalf("identifiers = %v, want only a DOI", ids)
	}
	if ids[0].Scheme != "DOI" || ids[0].Value != "10.1234/example" {
		t.Errorf("identifier = %+v", ids[0])
	}
}

func TestIsISBNSubjectRecognizesPlaceholder(t *testing.T) {
	if !isISBNSubject("978-0-000000-00-2") {
		t.Error("placeholder ISBN should still be recognized as an ISBN subject")
	}
}

func TestCleanReader(t *testing.T) {
	reader := &cleanReader{reader: strings.NewReader("A\x00B\fC\n")}
	got := make([]byte, 16)
	n, err := reader.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:n]) != "ABC\n" {
		t.Errorf("cleaned = %q, want %q", string(got[:n]), "ABC\n")
	}
}
