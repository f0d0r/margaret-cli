package txt

import (
	"slices"
	"testing"
)

// TestHungarianTitlePage pins the Hungarian fallback conventions observed in
// the corpus: author/title in either order, one-line pairs, NNCL headers and
// names or titles wrapped over two lines. The fallback only fills empty fields
// and never guesses when both orientations are equally plausible.
func TestHungarianTitlePage(t *testing.T) {
	cases := []struct {
		name    string
		head    string
		title   string
		authors []string
	}{
		{
			name:    "author then title",
			head:    "Karinthy Frigyes\nStruggle for life\n\nA reggel hűvös volt.\n",
			title:   "Struggle for life",
			authors: []string{"Karinthy Frigyes"},
		},
		{
			name:    "title then author",
			head:    "Struggle for life\nKarinthy Frigyes\n\nA reggel hűvös volt.\n",
			title:   "Struggle for life",
			authors: []string{"Karinthy Frigyes"},
		},
		{
			name:    "one line author colon title",
			head:    "Evelyn Marsh: A máltai orvos\n\nAz angol orvos évtizedekkel ezelőtt Máltán telepedett le.\n",
			title:   "A máltai orvos",
			authors: []string{"Evelyn Marsh"},
		},
		{
			name:    "nncl header before pair",
			head:    "NNCL1132-484v1.0\n\n\nEvelyn Marsh: A máltai orvos\n\nAz angol orvos évtizedekkel ezelőtt Máltán telepedett le.\n",
			title:   "A máltai orvos",
			authors: []string{"Evelyn Marsh"},
		},
		{
			name:    "one line author dash title",
			head:    "EVELYN MARSH - Őrjítő szenvedély\n\n\nA borító Boros Zoltán és Szikszai Gábor munkája\n\n© Mocsári Erika, 1996\n",
			title:   "Őrjítő szenvedély",
			authors: []string{"EVELYN MARSH"},
		},
		{
			name:    "body sentence is not an author",
			head:    "Philip K. Dick - Az ingázó\n\n\nAz emberke fáradt volt. Lassan átverekedte magát a tömegen.\n",
			title:   "Az ingázó",
			authors: []string{"Philip K. Dick"},
		},
		{
			name:  "separator is not an author",
			head:  "Favágó\n\n\n--------------------------------------------------------------------------------\n\nEgy nap a favágó egy ágat kezdett el levágni.\n",
			title: "",
		},
		{
			name:    "name split over two lines",
			head:    "Karinthy\nFrigyes\nStruggle for life\n\nA reggel hűvös volt.\n",
			title:   "Struggle for life",
			authors: []string{"Karinthy Frigyes"},
		},
		{
			name:    "title split over two lines",
			head:    "Karinthy Frigyes\nAz\narany ember\n\nA reggel hűvös volt.\n",
			title:   "Az arany ember",
			authors: []string{"Karinthy Frigyes"},
		},
		{
			name:  "ambiguous order is not guessed",
			head:  "Moby Dick\nHerman Melville\n\nCall me Ishmael and tell me your story.\n",
			title: "",
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

// TestHungarianFallbackRespectsOtherSources pins that the fallback never
// overrides YAML front matter or a Project Gutenberg preamble.
func TestHungarianFallbackRespectsOtherSources(t *testing.T) {
	cases := []struct {
		name    string
		head    string
		title   string
		authors []string
	}{
		{
			name:    "yaml wins",
			head:    "---\ntitle: Metadata Title\nauthor: Jane Doe\n---\n\nKarinthy Frigyes\nStruggle for life\n",
			title:   "Metadata Title",
			authors: []string{"Jane Doe"},
		},
		{
			name: "gutenberg preamble wins",
			head: "*** START OF THE PROJECT GUTENBERG EBOOK METADATA TITLE ***\n\n" +
				"Karinthy Frigyes\nStruggle for life\n",
			title: "METADATA TITLE",
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
