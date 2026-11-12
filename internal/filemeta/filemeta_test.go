package filemeta

import "testing"

func TestFilenameParsers(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		title  string
		author string
		kind   string
	}{
		{
			name:   "calibre",
			path:   "Great Expectations - Dickens, Charles.epub",
			title:  "Great Expectations",
			author: "Dickens, Charles",
			kind:   "calibre",
		},
		{
			name:   "natural author first",
			path:   "Herman Melville - Moby-Dick.epub",
			title:  "Moby-Dick",
			author: "Herman Melville",
			kind:   "natural",
		},
		{
			name:   "archive member",
			path:   "library.zip!books/Herman Melville - Moby-Dick.epub",
			title:  "Moby-Dick",
			author: "Herman Melville",
			kind:   "natural",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var title, author string
			var ok bool
			switch test.kind {
			case "calibre":
				title, author, ok = ParseCalibreFilename(test.path)
			default:
				title, author, ok = ParseNaturalFilename(test.path)
			}
			if !ok || title != test.title || author != test.author {
				t.Fatalf("parse = %q, %q, %v; want %q, %q, true", title, author, ok, test.title, test.author)
			}
		})
	}
}

func TestArchiveNames(t *testing.T) {
	path := "/books/Blatty, William Peter - Az \uFFFDrd\uFFFDg\uFFFDz\uFFFD.zip!az_ordoguzo.rtf"
	names := ArchiveNames(path)
	if len(names) != 1 {
		t.Fatalf("ArchiveNames = %v", names)
	}
	title, author, ok := ParseArchiveFilename(names[0])
	if !ok || author != "Blatty, William Peter" || title == "" {
		t.Fatalf("parse = %q, %q, %v", title, author, ok)
	}
}

func TestArchiveNamesBrokenBytes(t *testing.T) {
	path := string([]byte("/books/Blatty, William Peter - Az \x94rd\x94g\xfbz\x8b.zip!az_ordoguzo.rtf"))
	names := ArchiveNames(path)
	if len(names) != 1 {
		t.Fatalf("ArchiveNames = %v", names)
	}
	title, author, ok := ParseArchiveFilename(names[0])
	if !ok || author != "Blatty, William Peter" || title == "" {
		t.Fatalf("parse = %q, %q, %v", title, author, ok)
	}
}

func TestRepairName(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"cp437 filename", []byte("Krasznahorkai L\xa0szl\xa2 - S\xa0t\xa0ntang\xa2.pdf"), "Krasznahorkai László - Sátántangó.pdf"},
		{"cp1250 filename", []byte("S\xe1t\xe1ntang\xf3.pdf"), "Sátántangó.pdf"},
		{"cp852 archive", []byte("Az \x94rd\x94g\xfbz\x8b.zip"), "Az ördögűző.zip"},
		{"valid utf8", []byte("Regények/Krasznahorkai László.pdf"), "Regények/Krasznahorkai László.pdf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RepairName(string(test.in)); got != test.want {
				t.Fatalf("RepairName = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRepairDisplayPathMixed(t *testing.T) {
	path := string([]byte("/home/attila/books/Reg\xc3\xa9nyek/K/Krasznahorkai L\xc3\xa1szl\xc3\xb3/Krasznahorkai L\xa0szl\xa2 - S\xa0t\xa0ntang\xa2.pdf"))
	want := "/home/attila/books/Regények/K/Krasznahorkai László/Krasznahorkai László - Sátántangó.pdf"
	if got := RepairDisplayPath(path); got != want {
		t.Fatalf("RepairDisplayPath = %q, want %q", got, want)
	}
}

func TestParentAuthor(t *testing.T) {
	path := "/home/attila/books/Regények/K/Krasznahorkai László/Krasznahorkai L\xa0szl\xa2 - S\xa0t\xa0ntang\xa2.pdf"
	if got, ok := ParentAuthor(path); !ok || got != "Krasznahorkai László" {
		t.Fatalf("ParentAuthor = %q, %v", got, ok)
	}
	if got, ok := ParentAuthor("/home/attila/books/Regények/K/file.pdf"); ok {
		t.Fatalf("generic category folder must not be an author: %q", got)
	}
	if got, ok := ParentAuthor("/home/attila/books/Blatty, William Peter - Az.zip!member.rtf"); ok {
		t.Fatalf("archive container must not be a parent author: %q", got)
	}
}

func TestParseArchiveFilename(t *testing.T) {
	title, author, ok := ParseArchiveFilename("Blatty, William Peter - Az \uFFFDrd\uFFFDg\uFFFDz\uFFFD.zip")
	if !ok || author != "Blatty, William Peter" {
		t.Fatalf("archive parse = %q, %q, %v", title, author, ok)
	}
	if title == "" {
		t.Fatal("expected a title candidate even with broken runes")
	}
	title, author, ok = ParseArchiveFilename("Herman Melville - Moby-Dick.zip")
	if !ok || title != "Moby-Dick" || author != "Herman Melville" {
		t.Fatalf("archive parse = %q, %q, %v", title, author, ok)
	}
}

func TestTitleFromPath(t *testing.T) {
	if got := TitleFromPath("books/Moby_Dick.epub"); got != "Moby Dick" {
		t.Fatalf("TitleFromPath = %q", got)
	}
	if got := TitleFromPath("outer.zip!inner/c.epub"); got != "c" {
		t.Fatalf("archive member title = %q", got)
	}
}
