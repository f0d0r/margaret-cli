package txt

import (
	"slices"
	"testing"
)

func TestExtractMetadata(t *testing.T) {
	cases := []struct {
		name    string
		head    string
		title   string
		authors []string
	}{
		{
			name:    "labeled header",
			head:    "Title: The Great Novel\nAuthor: Jane Doe\n\nBody text.\n",
			title:   "The Great Novel",
			authors: []string{"Jane Doe"},
		},
		{
			name:    "multiple authors label",
			head:    "Title: The Great Novel\nAuthors: Jane Doe & John Smith\n\nBody.\n",
			title:   "The Great Novel",
			authors: []string{"Jane Doe", "John Smith"},
		},
		{
			name:    "by label",
			head:    "Title: The Great Novel\nBy: Jane Doe and John Smith\n",
			title:   "The Great Novel",
			authors: []string{"Jane Doe", "John Smith"},
		},
		{
			name:    "creator label",
			head:    "Title: The Great Novel\nCreator: Jane Doe with John Smith\n",
			title:   "The Great Novel",
			authors: []string{"Jane Doe", "John Smith"},
		},
		{
			name:    "legacy ti au header",
			head:    "TI: Erdélyi József, a feltámadt Petőfi\nTI: Piros Pünkösd\nAU: Károlyi Amy\n\nA vers szövege.\n",
			title:   "Erdélyi József, a feltámadt Petőfi",
			authors: []string{"Károlyi Amy"},
		},
		{
			name:    "legacy ti au header with unknown fields",
			head:    "TI: Zöld barackok\nAU: Karinthy Frigyes\nNA: Kiss Gábor (begépelő)\nDT: vers\nSO: http://example.org/x\n\nA novella szövege.\n",
			title:   "Zöld barackok",
			authors: []string{"Karinthy Frigyes"},
		},
		{
			name:    "calibre title page",
			head:    "Moby Dick\n\n\nHerman Melville\nCall me Ishmael.\n",
			title:   "Moby Dick",
			authors: []string{"Herman Melville"},
		},
		{
			name: "gutenberg preamble",
			head: "The Project Gutenberg eBook of Moby Dick\n\n" +
				"This eBook is for the use of anyone anywhere.\n\n" +
				"Title: Moby Dick\n\nAuthor: Herman Melville\n\n" +
				"*** START OF THE PROJECT GUTENBERG EBOOK MOBY DICK ***\n\nCHAPTER 1.\n",
			title:   "Moby Dick",
			authors: []string{"Herman Melville"},
		},
		{
			name:  "gutenberg marker title",
			head:  "*** START OF THE PROJECT GUTENBERG EBOOK MOBY DICK ***\n\nCHAPTER 1.\n",
			title: "MOBY DICK",
		},
		{
			name:    "yaml front matter",
			head:    "---\ntitle: \"The Great Novel\"\nauthor: [Jane Doe, John Smith]\n---\n\nBody.\n",
			title:   "The Great Novel",
			authors: []string{"Jane Doe", "John Smith"},
		},
		{
			name:    "last first comma preserved",
			head:    "Title: The Great Novel\nAuthor: Smith, John\n",
			title:   "The Great Novel",
			authors: []string{"Smith, John"},
		},
		{
			name:  "crlf line endings",
			head:  "Title: The Great Novel\r\nAuthor: Jane Doe\r\n\r\nBody.\r\n",
			title: "The Great Novel", authors: []string{"Jane Doe"},
		},
		{
			name:  "placeholder dropped",
			head:  "Title: Untitled\nAuthor: Unknown\n\nBody.\n",
			title: "",
		},
		{
			name:  "label after body ignored",
			head:  "Chapter One\n\nTitle: Not A Header\n",
			title: "",
		},
		{
			name:  "blank leading lines",
			head:  "\n\nTitle: The Great Novel\nAuthor: Jane Doe\n\nBody.\n",
			title: "The Great Novel", authors: []string{"Jane Doe"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md := extractMetadata(c.head)
			if md.Title != c.title {
				t.Errorf("title = %q, want %q", md.Title, c.title)
			}
			if !slices.Equal(md.Authors, c.authors) {
				t.Errorf("authors = %v, want %v", md.Authors, c.authors)
			}
		})
	}
}

func TestSplitAuthors(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Jane Doe", []string{"Jane Doe"}},
		{"Jane Doe & John Smith", []string{"Jane Doe", "John Smith"}},
		{"Jane Doe and John Smith", []string{"Jane Doe", "John Smith"}},
		{"Jane Doe, and John Smith", []string{"Jane Doe", "John Smith"}},
		{"Jane Doe with John Smith", []string{"Jane Doe", "John Smith"}},
		{"Smith, John", []string{"Smith, John"}},
		{"A; B", []string{"A", "B"}},
		{"[Jane Doe, John Smith]", []string{"Jane Doe", "John Smith"}},
		{"Unknown", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := splitAuthors(c.in); !slices.Equal(got, c.want) {
			t.Errorf("splitAuthors(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
