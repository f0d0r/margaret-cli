package pdf

import "testing"

func TestDecodePDFText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "utf16be bom",
			in:   `\376\377\000J\000e\000f\000f\000_\000L\000i\000n\000d\000s\000a\000y`,
			want: "Jeff_Lindsay",
		},
		{
			name: "utf16be author",
			in:   `\376\377\000b\000b`,
			want: "bb",
		},
		{
			name: "utf16be accented",
			in:   `\376\377\000R\000e\000m\000\351\000n\000y`,
			want: "Remény",
		},
		{
			name: "utf16le bom",
			in:   `\377\376J\000e\000`,
			want: "Je",
		},
		{
			name: "utf16be no bom",
			in:   `\000J\000e`,
			want: "Je",
		},
		{
			name: "pdfdocencoding",
			in:   `Microsoft Word - A csillagok ak\341r a por`,
			want: "Microsoft Word - A csillagok akár a por",
		},
		{
			name: "escaped parentheses",
			in:   `\050test\051`,
			want: "(test)",
		},
		{
			name: "plain text",
			in:   "Moby Dick",
			want: "Moby Dick",
		},
		{
			name: "windows path stays",
			in:   `C:\temp\file`,
			want: `C:\temp\file`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decodePDFText(test.in); got != test.want {
				t.Fatalf("decodePDFText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseXMPDecodesPDFEscapes(t *testing.T) {
	raw := `<?xml version="1.0"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
           xmlns:dc="http://purl.org/dc/elements/1.1/">
    <rdf:Description>
      <dc:title><rdf:Alt><rdf:li xml:lang="x-default">\376\377\000M\000o\000b\000y\000 \000D\000i\000c\000k</rdf:li></rdf:Alt></dc:title>
      <dc:creator><rdf:Seq><rdf:li>\376\377\000H\000e\000r\000m\000a\000n\000 \000M\000e\000l\000v\000i\000l\000l\000e</rdf:li></rdf:Seq></dc:creator>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>`
	md, err := parseXMP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if md.title != "Moby Dick" {
		t.Fatalf("title = %q", md.title)
	}
	if len(md.authors) != 1 || md.authors[0] != "Herman Melville" {
		t.Fatalf("authors = %v", md.authors)
	}
}

func TestParseInfoDecodesEscapedAuthor(t *testing.T) {
	info := parseInfo("Title: \\376\\377\\000M\\000o\\000b\\000y\\000 \\000D\\000i\\000c\\000k\nAuthor: \\376\\377\\000b\\000b\n")
	md := metadataFromInfo(info)
	if md.title != "Moby Dick" {
		t.Fatalf("title = %q", md.title)
	}
	if len(md.authors) != 1 || md.authors[0] != "bb" {
		t.Fatalf("authors = %v", md.authors)
	}
}
