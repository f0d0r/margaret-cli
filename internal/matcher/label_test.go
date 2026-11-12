package matcher

import "testing"

func TestBookLabel(t *testing.T) {
	if got := bookLabel(&Book{Title: "The Hobbit", Authors: []string{"Tolkien"}}); got != "Tolkien: The Hobbit" {
		t.Fatalf("label = %q", got)
	}
	if got := bookLabel(&Book{Title: "The Hobbit", Authors: []string{"Tolkien, Christopher"}}); got != "Tolkien: The Hobbit" {
		t.Fatalf("label with multiple authors = %q", got)
	}
	if got := bookLabel(&Book{Title: "The Hobbit"}); got != "The Hobbit" {
		t.Fatalf("label without author = %q", got)
	}
	if got := bookLabel(&Book{}); got != "(untitled)" {
		t.Fatalf("label without title = %q", got)
	}
	if got := bookLabel(nil); got != "" {
		t.Fatalf("nil label = %q", got)
	}
}
