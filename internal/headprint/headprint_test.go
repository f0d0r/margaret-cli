package headprint

import (
	"testing"

	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

func TestBuildAndContainment(t *testing.T) {
	bloom := Build("The Hobbit by J.R.R. Tolkien")
	if len(bloom) != BloomBytes {
		t.Fatalf("bloom length = %d, want %d", len(bloom), BloomBytes)
	}
	if got := Containment(bloom, textmatch.ContentTokens("The Hobbit")); got != 1 {
		t.Fatalf("title containment = %f, want 1", got)
	}
	if got := Containment(bloom, textmatch.AuthorTokens("J.R.R. Tolkien")); got != 1 {
		t.Fatalf("author containment = %f, want 1", got)
	}
	if got := Containment(bloom, textmatch.ContentTokens("Dune")); got != 0 {
		t.Fatalf("missing title containment = %f, want 0", got)
	}
}

func TestEmptyBuildIsUsable(t *testing.T) {
	bloom := Build("")
	if len(bloom) != BloomBytes {
		t.Fatalf("bloom length = %d, want %d", len(bloom), BloomBytes)
	}
	if got := Containment(bloom, []string{"anything"}); got != 0 {
		t.Fatalf("containment = %f, want 0", got)
	}
	if Contains(nil, "anything") {
		t.Fatal("nil bloom contains a token")
	}
}
