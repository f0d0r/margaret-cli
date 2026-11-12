package claim

import "testing"

const titlePage = "Title: Moby Dick\nAuthor: Herman Melville\n\n*** START OF THE PROJECT GUTENBERG EBOOK ***\n\nCall me Ishmael."

func TestAttest(t *testing.T) {
	tests := []struct {
		name   string
		field  Field
		value  string
		ok     bool
		method string
	}{
		{"title exact", FieldTitle, "Moby Dick", true, "primary_exact"},
		{"author exact", FieldAuthor, "Herman Melville", true, "primary_exact"},
		{"title generic absent", FieldTitle, "Linux Kernel", false, "none"},
		{"author surname only", FieldAuthor, "Smith", false, "none"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ok, _, method := Attest(titlePage, test.field, test.value)
			if ok != test.ok || method != test.method {
				t.Fatalf("Attest() = %v, %q; want %v, %q", ok, method, test.ok, test.method)
			}
		})
	}
}

func TestWindowLinesStopsAtBody(t *testing.T) {
	head := "PONT EGY PONTY!\nJohn Caldwell\nSoha ezelőtt nem szólalt még meg ennyi jelző egyszerre!\n"
	lines := WindowLines(head)
	if len(lines) != 2 {
		t.Fatalf("window = %v, want title and author only", lines)
	}
	if lines[0] != "PONT EGY PONTY!" || lines[1] != "John Caldwell" {
		t.Fatalf("window = %v", lines)
	}
}

func TestResolveMetadataAndTextCorroborate(t *testing.T) {
	claims := Build([]Set{
		metaSet("Moby Dick", "Herman Melville"),
		textSet("Moby Dick", "Herman Melville"),
	}, titlePage, "book.epub")
	res := Resolve(claims, "book.epub")
	if res.Title != "Moby Dick" || res.TitleStatus != StatusCorroborated {
		t.Fatalf("title = %q, status = %q", res.Title, res.TitleStatus)
	}
	if len(res.Authors) != 1 || res.AuthorStatus != StatusCorroborated {
		t.Fatalf("authors = %v, status = %q", res.Authors, res.AuthorStatus)
	}
}

func TestResolveMetadataAndFilenameAreSingle(t *testing.T) {
	claims := Build([]Set{
		metaSet("Moby Dick", "Herman Melville"),
		filenameSet("Moby Dick", "Herman Melville"),
	}, "", "book.epub")
	res := Resolve(claims, "book.epub")
	if res.TitleStatus != StatusSingle {
		t.Fatalf("title status = %q, want single", res.TitleStatus)
	}
}

func TestResolveFilenameOnlyIsWeak(t *testing.T) {
	claims := Build([]Set{filenameSet("Moby Dick", "Herman Melville")}, "", "book.epub")
	res := Resolve(claims, "book.epub")
	if res.TitleStatus != StatusWeak || res.AuthorStatus != StatusWeak {
		t.Fatalf("statuses = %q/%q, want weak/weak", res.TitleStatus, res.AuthorStatus)
	}
	if res.Title != "" || len(res.Authors) != 0 {
		t.Fatalf("filename-only weak values must stay empty, got %q / %v", res.Title, res.Authors)
	}
}

func TestResolveStrongConflict(t *testing.T) {
	claims := Build([]Set{
		metaSet("Moby Dick", "Herman Melville"),
		textSet("The Whale", "Herman Melville"),
	}, titlePage, "book.epub")
	res := Resolve(claims, "book.epub")
	if res.TitleStatus != StatusConflict {
		t.Fatalf("title status = %q, want conflict", res.TitleStatus)
	}
}

func TestResolveWeakHypothesisDoesNotConflict(t *testing.T) {
	claims := Build([]Set{
		metaSet("Moby Dick", "Herman Melville"),
		{
			Key: "text:titlepage", Source: SourceText, Origin: "text:titlepage",
			Title: "The Whale", Trust: TrustWeak, Witness: WitnessSupporting,
		},
	}, titlePage, "book.epub")
	res := Resolve(claims, "book.epub")
	if res.TitleStatus == StatusConflict {
		t.Fatalf("weak title hypothesis must not create a conflict")
	}
	if res.Title != "Moby Dick" {
		t.Fatalf("title = %q, want metadata title", res.Title)
	}
}

func TestResolveMultiAuthorMetadataDoesNotConflict(t *testing.T) {
	claims := Build([]Set{
		{
			Key: "metadata", Source: SourceMetadata, Origin: "test",
			Title: "Good Omens", Authors: []string{"Terry Pratchett", "Neil Gaiman"},
			Trust: TrustStrong, Witness: WitnessIndependent,
		},
	}, titlePage, "book.epub")
	res := Resolve(claims, "book.epub")
	if res.AuthorStatus == StatusConflict {
		t.Fatalf("multi-author metadata must not conflict with itself")
	}
	if len(res.Authors) != 2 {
		t.Fatalf("authors = %v, want two names", res.Authors)
	}
}

func metaSet(title, author string) Set {
	return Set{
		Key: "metadata", Source: SourceMetadata, Origin: "test",
		Title: title, Authors: []string{author},
		Trust: TrustStrong, Witness: WitnessIndependent,
	}
}

func textSet(title, author string) Set {
	return Set{
		Key: "text:label", Source: SourceText, Origin: "text:label",
		Title: title, Authors: []string{author},
		Trust: TrustStrong, Witness: WitnessIndependent,
	}
}

func filenameSet(title, author string) Set {
	return Set{
		Key: "filename:natural", Source: SourceFilename, Origin: "filename:natural",
		Title: title, Authors: []string{author},
		Trust: TrustWeak, Witness: WitnessSupporting,
	}
}
