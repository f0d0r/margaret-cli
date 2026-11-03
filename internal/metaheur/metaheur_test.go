package metaheur

import "testing"

func TestScoreName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"two tokens", "Herman Melville", true},
		{"natural order", "Móricz Zsigmond", true},
		{"middle initial", "Philip K. Dick", true},
		{"initials", "J. R. R. Tolkien", true},
		{"last first", "Smith, John", true},
		{"particles", "van der Meer, Jan", true},
		{"hyphenated", "Móricz-Szabó Zsolt", true},
		{"credit", "A borító Boros Zoltán és Szikszai Gábor munkája", false},
		{"cover credit", "Szikszai Gábor munkája", false},
		{"author and title", "Evelyn Marsh: A máltai orvos", false},
		{"separator", "--------------------------------------------------------------------------------", false},
		{"title with article", "A máltai orvos", false},
		{"title inversion", "Great Slow Kings, The", false},
		{"too many words", "A B C D E F G", false},
		{"lowercase word", "Herman melville", false},
		{"digits", "John Smith 2", false},
		{"mononym", "Petőfi", false},
		{"organization", "Vakok Elektronikus Könyvtára", false},
		{"institute", "Digitális Irodalmi Akadémia", false},
		{"body sentence", "Call me Ishmael and tell me your story.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsName(c.in); got != c.want {
				t.Errorf("IsName(%q) = %v, want %v (score %d)", c.in, got, c.want, ScoreName(c.in))
			}
		})
	}
}

func TestScoreTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"with article", "A máltai orvos", true},
		{"with article and lowercase", "Az arany ember", true},
		{"single word", "Favágó", true},
		{"title inversion", "Great Slow Kings, The", true},
		{"long title", "Bűn és bűnhődés", true},
		{"year title", "1984", true},
		{"pure name", "Herman Melville", false},
		{"pure name two tokens", "Moby Dick", false},
		{"credit", "A borító Boros Zoltán munkája", false},
		{"chapter", "Első fejezet", false},
		{"numeral chapter", "IV.", false},
		{"nncl", "NNCL1132-484v1.0", false},
		{"isbn", "ISBN 978-3-16-148410-0", false},
		{"url", "https://example.com/book", false},
		{"separator", "-----", false},
		{"prose", "Ez egy nagyon hosszú mondat arról, hogy mi történt a faluban azon a nyáron.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTitle(c.in); got != c.want {
				t.Errorf("IsTitle(%q) = %v, want %v (score %d)", c.in, got, c.want, ScoreTitle(c.in))
			}
		})
	}
}

func TestIsCredit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"cover", "A borító Boros Zoltán munkája", true},
		{"translator", "Fordította: Kovács Béla", true},
		{"publisher colon", "Kiadó: Magvető", true},
		{"publisher name", "Magvető Kiadó", true},
		{"publisher in title", "A kiadó", false},
		{"digitization", "Digitalizálta a MEK", true},
		{"title", "A máltai orvos", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCredit(c.in); got != c.want {
				t.Errorf("IsCredit(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
