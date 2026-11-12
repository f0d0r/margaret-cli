package textmatch

import (
	"slices"
	"testing"
)

func TestTitleKey(t *testing.T) {
	tests := map[string]string{
		"  The   Hobbit, or There and Back Again  ": "the hobbit or there and back again",
		"Márai Sándor":             "marai sandor",
		"Moby-Dick; or, The Whale": "moby dick or the whale",
		"1984":                     "1984",
		"Æon Flux":                 "aeon flux",
		"Straße":                   "strasse",
		"Café":                     "cafe",
		"«Тихий Дон»":              "тихий дон",
		"Война и мир":              "война и мир",
		"...":                      "",
	}
	for input, want := range tests {
		if got := TitleKey(input); got != want {
			t.Errorf("TitleKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTitleTokens(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"The Hobbit", []string{"hobbit", "the"}},
		{"Moby-Dick; or, The Whale", []string{"dick", "moby", "or", "the", "whale"}},
		{"1984 1984", []string{"1984"}},
		{"", nil},
	}
	for _, test := range tests {
		if got := TitleTokens(test.input); !slices.Equal(got, test.want) {
			t.Errorf("TitleTokens(%q) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestAuthorTokens(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"J. K. Rowling", []string{"rowling"}},
		{"Rowling, J. K.", []string{"rowling"}},
		{"George R. R. Martin", []string{"george", "martin"}},
		{"Márai Sándor", []string{"marai", "sandor"}},
		{"van der Berg", []string{"berg"}},
		{"O'Brien", []string{"brien"}},
		{"王小明", []string{"王小明"}},
		{"", nil},
	}
	for _, test := range tests {
		if got := AuthorTokens(test.input); !slices.Equal(got, test.want) {
			t.Errorf("AuthorTokens(%q) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestJaccard(t *testing.T) {
	tests := []struct {
		a, b []string
		want float64
	}{
		{[]string{"a", "b"}, []string{"a", "b"}, 1},
		{[]string{"a", "b"}, []string{"c"}, 0},
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}, 0.5},
		{[]string{}, []string{"a"}, 0},
		{[]string{}, []string{}, 0},
	}
	for _, test := range tests {
		if got := Jaccard(test.a, test.b); got != test.want {
			t.Errorf("Jaccard(%v, %v) = %f, want %f", test.a, test.b, got, test.want)
		}
	}
}

func TestASCIINormalization(t *testing.T) {
	if got := TitleKey("  The   Hobbit!  "); got != "the hobbit" {
		t.Fatalf("TitleKey = %q", got)
	}
	if got := TitleKey("Café"); got != "cafe" {
		t.Fatalf("TitleKey unicode = %q", got)
	}
	if got := TokensFromKey(TitleKey("A B")); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("TokensFromKey = %v", got)
	}
	if got := AuthorTokens("J.R.R. Tolkien"); len(got) != 1 || got[0] != "tolkien" {
		t.Fatalf("AuthorTokens = %v", got)
	}
	if got := AuthorTokens("van der Waals"); len(got) != 1 || got[0] != "waals" {
		t.Fatalf("AuthorTokens particles = %v", got)
	}
}

func TestJaccardHashedMatchesStrings(t *testing.T) {
	cases := []struct {
		a, b []string
	}{
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"c"}},
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}},
		{nil, []string{"a"}},
	}
	for _, test := range cases {
		want := Jaccard(test.a, test.b)
		got := JaccardHashed(HashTokens(test.a), HashTokens(test.b))
		if got != want {
			t.Fatalf("JaccardHashed(%v, %v) = %f, want %f", test.a, test.b, got, want)
		}
	}
}
