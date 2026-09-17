package report

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/database"
	"github.com/f0d0r/margaret-cli/internal/db"
)

func TestWriteBooksEmpty(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(db.New(conn), path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written, stat err = %v", err)
	}
}

func TestPickTitle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []string
		authors    []string
		want       string
	}{
		{"majority beats longer", []string{"Moby Dick", "Moby Dick; Or, The Whale", "Moby Dick"}, nil, "Moby Dick"},
		{"tie goes to longer", []string{"Moby Dick", "Moby Dick; Or, The Whale", "Moby-Dick"}, nil, "Moby Dick; Or, The Whale"},
		{"tie goes to cleaner over longer junk", []string{"lll__a_megfojtott_viking_mocsara", "A megfojtott viking mocsara"}, nil, "A megfojtott viking mocsara"},
		{"junk majority still wins", []string{"lll__a_megfojtott_viking_mocsara", "lll__a_megfojtott_viking_mocsara", "A megfojtott viking mocsara"}, nil, "lll__a_megfojtott_viking_mocsara"},
		{"tie goes to title without author", []string{"Steven Saylor - Egy gladiátor csak egyszer hal meg", "Egy gladiátor csak egyszer hal meg"}, []string{"Steven Saylor"}, "Egy gladiátor csak egyszer hal meg"},
		{"noisy majority still wins", []string{"Steven Saylor - X", "Steven Saylor - X", "X"}, []string{"Steven Saylor"}, "Steven Saylor - X"},
		{"last first author variant detected", []string{"Steven Saylor - X", "X"}, []string{"Saylor, Steven"}, "X"},
		{"reversed comma order detected", []string{"DEAVER , JEFFERY - A majomkirály", "A majomkirály"}, []string{"Jeffery Deaver"}, "A majomkirály"},
		{"four part name in any order", []string{"García Márquez, Gabriel - Száz év magány", "Száz év magány"}, []string{"Gabriel García Márquez"}, "Száz év magány"},
		{"particle alone is not enough, length decides", []string{"Van valami", "X"}, []string{"Ludwig van Beethoven"}, "Van valami"},
		{"partial name without first name is not enough", []string{"Van Beethoven - X", "X"}, []string{"Ludwig van Beethoven"}, "Van Beethoven - X"},
		{"initial drops out, surname decides", []string{"Verne - X", "X"}, []string{"J. Verne"}, "X"},
		{"diacritics folded in author match", []string{"Marai Sandor - X", "X"}, []string{"Márai Sándor"}, "X"},
		{"surname alone is not penalized, longer wins", []string{"Saylor blabla", "X"}, []string{"Steven Saylor"}, "Saylor blabla"},
		{"empty dropped", []string{"", "Moby Dick"}, nil, "Moby Dick"},
		{"placeholder dropped", []string{"Untitled", "It"}, nil, "It"},
		{"all placeholders", []string{"Untitled", "Sans titre"}, nil, ""},
		{"multilingual placeholder loses", []string{"無題", "Dune"}, nil, "Dune"},
		{"all empty", []string{"", "  "}, nil, ""},
		{"tie same length goes to first seen", []string{"AB", "CD"}, nil, "AB"},
		{"whitespace and case fold together", []string{"  Moby   Dick ", "moby dick"}, nil, "Moby Dick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickTitle(tc.candidates, tc.authors); got != tc.want {
				t.Errorf("pickTitle(%q) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestPickAuthors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates [][]string
		want       []string
	}{
		{"placeholder filtered", [][]string{{"Ismeretlen"}, {"J. Verne"}}, []string{"J. Verne"}},
		{"placeholder inside list filtered", [][]string{{"Ismeretlen", "Herman Melville"}}, []string{"Herman Melville"}},
		{"majority list wins", [][]string{{"A"}, {"A"}, {"B"}}, []string{"A"}},
		{"order insensitive vote keeps first seen order", [][]string{{"A", "B"}, {"B", "A"}}, []string{"A", "B"}},
		{"tie goes to longer list", [][]string{{"A"}, {"A", "B"}}, []string{"A", "B"}},
		{"tie goes to spaced name", [][]string{{"ABC"}, {"A B"}}, []string{"A B"}},
		{"tie same goes to first seen", [][]string{{"AB"}, {"CD"}}, []string{"AB"}},
		{"all placeholders", [][]string{{"Unknown"}, {"Ismeretlen"}}, []string{}},
		{"user is a placeholder", [][]string{{"User"}}, []string{}},
		{"comma order votes together, first seen display", [][]string{{"Christie, Agatha"}, {"Agatha Christie"}}, []string{"Christie, Agatha"}},
		{"comma order votes together either way", [][]string{{"Agatha Christie"}, {"Christie, Agatha"}, {"Agatha Christie"}}, []string{"Agatha Christie"}},
		{"no candidates", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickAuthors(tc.candidates); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pickAuthors(%q) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestWriteBooks(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "Moby Dick")
	if err != nil {
		t.Fatal(err)
	}

	addFile := func(hash, path, title string, authors []string) {
		t.Helper()
		fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
			Hash:  hash,
			Path:  path,
			Title: title,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
			BookID:     bookID,
			BookFileID: fileID,
		}); err != nil {
			t.Fatal(err)
		}
		for _, name := range authors {
			if err := q.CreateAuthor(ctx, name); err != nil {
				t.Fatal(err)
			}
			author, err := q.GetAuthorByName(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
				BookFileID: fileID,
				AuthorID:   author.ID,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	addFile("hash1", "books/mobydick.epub", "Moby Dick", []string{"Herman Melville"})
	addFile("hash2", "books/mobydick.mobi", "Moby Dick", []string{"Herman Melville"})
	addFile("hash3", "books/mobydick.pdf", "Moby-Dick", []string{"Ismeretlen"})
	if err := q.CreateBookFileDuplicate(ctx, db.CreateBookFileDuplicateParams{
		Hash: "hash1",
		Path: "backup/mobydick.epub",
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "Moby Dick" {
		t.Errorf("expected title %q, got %q", "Moby Dick", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Herman Melville"}) {
		t.Errorf("expected authors [Herman Melville], got %q", b.Authors)
	}
	if len(b.Files) != 4 {
		t.Fatalf("expected 4 files (3 + 1 duplicate), got %d", len(b.Files))
	}
	dup := b.Files[3]
	if dup.Path != "backup/mobydick.epub" {
		t.Errorf("expected duplicate path, got %q", dup.Path)
	}
	if dup.Title != "Moby Dick" || !reflect.DeepEqual(dup.Authors, []string{"Herman Melville"}) {
		t.Errorf("expected duplicate to inherit parent metadata, got %+v", dup)
	}
}

func TestTitleFromPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"plain path", "/home/attila/books/ebooks/spring/Spring in Action 4th edition by Craig Walls.epub", "Spring in Action 4th edition by Craig Walls"},
		{"underscores become spaces", "books/Jules_Verne_20_000_Leagues.mobi", "Jules Verne 20 000 Leagues"},
		{"dots are kept, underscores become spaces", "books/J._Verne.epub", "J. Verne"},
		{"uppercase extension", "books/Dune.EPUB", "Dune"},
		{"nested archive member", "/tmp/bundle.zip!sub/inner.zip!c.epub", "c"},
		{"unknown extension kept", "books/notes.txt2", "notes.txt2"},
		{"no extension", "books/README", "README"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := titleFromPath(tc.path); got != tc.want {
				t.Errorf("titleFromPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestWriteBooksFallsBackToFileName(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "hash-no-title",
		Path:  "/home/attila/books/ebooks/spring/Spring in Action 4th edition by Craig Walls.epub",
		Title: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "Spring in Action 4th edition by Craig Walls" {
		t.Errorf("expected filename fallback title, got %q", b.Title)
	}
	if len(b.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(b.Files))
	}
	if b.Files[0].Title != "" {
		t.Errorf("expected file title to stay empty, got %q", b.Files[0].Title)
	}
	if b.Authors == nil || b.Files[0].Authors == nil {
		t.Errorf("expected non-nil author lists, got %+v", b)
	}
}

func TestFoldDiacritics(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"hungarian", "Márai Sándor", "marai sandor"},
		{"czech combining marks", "žůžo", "zuzo"},
		{"scandinavian slashed o", "Bjørn", "bjorn"},
		{"german eszett and umlaut", "Straße Müller", "strasse muller"},
		{"ligatures", "æ œ", "ae oe"},
		{"polish crossed l", "Łódź", "lodz"},
		{"plain ascii untouched", "Steven Saylor", "steven saylor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldName(tc.in); got != tc.want {
				t.Errorf("foldName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMatchTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"plain name", "Jeffery Deaver", []string{"jeffery", "deaver"}},
		{"comma separated", "Deaver, Jeffery", []string{"deaver", "jeffery"}},
		{"initials dropped", "J. Verne", []string{"verne"}},
		{"particles dropped", "Ludwig van Beethoven", []string{"ludwig", "beethoven"}},
		{"deduped", "Deaver Deaver", []string{"deaver"}},
		{"all dropped", "Al Di", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchTokens(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("matchTokens(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsPlaceholderAuthor(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"hungarian", "Ismeretlen", true},
		{"hungarian unaccented", "Szerzo ismeretlen", true},
		{"english", "Unknown Author", true},
		{"english case", "NO AUTHOR", true},
		{"french", "Inconnu", true},
		{"german", "Unbekannt", true},
		{"spanish", "Desconocido", true},
		{"portuguese", "Desconhecida", true},
		{"italian", "Autore Sconosciuto", true},
		{"dutch", "Onbekend", true},
		{"czech", "Neznámý", true},
		{"polish", "Autor Nieznany", true},
		{"swedish", "Okänd", true},
		{"turkish", "Bilinmiyor", true},
		{"russian", "Неизвестный автор", true},
		{"arabic", "مجهول", true},
		{"urdu", "نامعلوم", true},
		{"hindi", "अज्ञात", true},
		{"chinese anonymous", "佚名", true},
		{"chinese unknown", "未知作者", true},
		{"japanese", "作者不明", true},
		{"korean", "작자 미상", true},
		{"device default", "User", true},
		{"untitled as author", "Untitled", true},
		{"real name", "Jeffery Deaver", false},
		{"real cjk name", "村上春樹", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlaceholderAuthor(tc.in); got != tc.want {
				t.Errorf("isPlaceholderAuthor(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFoldDiacriticsKeepsNonLatinScripts(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		// Cyrillic й (и+breve) must survive: it is not a Latin diacritic.
		{"cyrillic short i", "Неизвестный", "неизвестный"},
		// Devanagari viramas (Mn) must survive: stripping them corrupts the script.
		{"devanagari intact", "अज्ञात", "अज्ञात"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldName(tc.in); got != tc.want {
				t.Errorf("foldName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsPlaceholderTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"english", "Untitled", true},
		{"english case", "UNTITLED", true},
		{"french", "Sans titre", true},
		{"german", "Ohne Titel", true},
		{"spanish", "Sin título", true},
		{"italian", "Senza titolo", true},
		{"russian", "Без названия", true},
		{"chinese", "无标题", true},
		{"japanese", "無題", true},
		{"korean", "제목 없음", true},
		{"hungarian", "Névtelen", true},
		{"real title", "Dune", false},
		{"title containing word", "The Untitled Project", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlaceholderTitle(tc.in); got != tc.want {
				t.Errorf("isPlaceholderTitle(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseCalibreFilename(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		wantTitle string
		wantAuth  string
		wantOK    bool
	}{
		{"standard", "/books/A romai nep tortenete 1 - Livius, Titus.epub", "A romai nep tortenete 1", "Livius, Titus", true},
		{"no comma on right", "/books/Dune - Frank Herbert.epub", "", "", false},
		{"no separator", "/books/Dune.epub", "", "", false},
		{"placeholder left", "/books/Untitled - Smith, John.epub", "", "", false},
		{"single token right", "/books/X - Smith.epub", "", "", false},
		{"too many tokens right", "/books/X - A, B, C, D, E.epub", "", "", false},
		{"archive member", "/tmp/pack.zip!A majomkiraly - Deaver, Jeffery.prc", "A majomkiraly", "Deaver, Jeffery", true},
		{"underscores cleaned", "/books/A_majomkiraly-Deaver,_Jeffery.epub", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title, auth, ok := parseCalibreFilename(tc.path)
			if title != tc.wantTitle || auth != tc.wantAuth || ok != tc.wantOK {
				t.Errorf("parseCalibreFilename(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.path, title, auth, ok, tc.wantTitle, tc.wantAuth, tc.wantOK)
			}
		})
	}
}

func TestWashAuthorAffix(t *testing.T) {
	saylor := [][]string{{"steven", "saylor"}}
	nagy := [][]string{{"nagy", "laszlo"}}
	for _, tc := range []struct {
		name string
		stem string
		sets [][]string
		want string
	}{
		{"trailing dash credit", "11 - Egy gladiátor csak egyszer hal meg - Steven Saylor", saylor, "11 - Egy gladiátor csak egyszer hal meg"},
		{"leading dash credit", "Steven Saylor - Egy gladiátor", saylor, "Egy gladiátor"},
		{"trailing by credit", "Spring in Action 4th edition by Craig Walls", [][]string{{"craig", "walls"}}, "Spring in Action 4th edition"},
		{"middle common word kept", "A nagy kaland", nagy, "A nagy kaland"},
		{"space separated trailing kept", "Valami Nagy", [][]string{{"nagy"}}, "Valami Nagy"},
		{"dash separated trailing stripped", "Valami - Nagy", [][]string{{"nagy"}}, "Valami"},
		{"no authors", "X - Y", nil, "X - Y"},
		{"empty stem", "", saylor, ""},
		{"whole stem is author kept", "Steven Saylor", saylor, "Steven Saylor"},
		{"genuine title containing author kept", "Stephen King Goes to the Movies", [][]string{{"stephen", "king"}}, "Stephen King Goes to the Movies"},
		{"placeholder result keeps stem", "John Smith - Untitled", [][]string{{"john", "smith"}}, "John Smith - Untitled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := washAuthorAffix(tc.stem, tc.sets); got != tc.want {
				t.Errorf("washAuthorAffix(%q) = %q, want %q", tc.stem, got, tc.want)
			}
		})
	}
}

func TestWriteBooksCalibreFallback(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "hash-livius",
		Path:  "/home/attila/books/Regények/L/Livius/A romai nep tortenete 1 - Livius, Titus.epub",
		Title: "Untitled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "A romai nep tortenete 1" {
		t.Errorf("expected Calibre title, got %q", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Livius, Titus"}) {
		t.Errorf("expected Calibre authors, got %q", b.Authors)
	}
	if len(b.Files) != 1 || b.Files[0].Title != "Untitled" {
		t.Errorf("expected file entry to keep original title, got %+v", b.Files)
	}
}

func TestWriteBooksWashAffix(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "hash-saylor",
		Path:  "/home/attila/books/11 - Egy gladiátor csak egyszer hal meg - Steven Saylor.epub",
		Title: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Steven Saylor"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Steven Saylor")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: fileID,
		AuthorID:   author.ID,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "11 - Egy gladiátor csak egyszer hal meg" {
		t.Errorf("expected washed title, got %q", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Steven Saylor"}) {
		t.Errorf("expected authors untouched, got %q", b.Authors)
	}
}

func TestWriteBooksWashPickedTitle(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "Marcus Meadow - Könnyek városa")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
		Hash:  "hash-meadow",
		Path:  "/home/attila/books/Regények/prc pack/Marcus Meadow - Konnyek varosa.prc",
		Title: "Marcus Meadow - Könnyek városa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
		BookID:     bookID,
		BookFileID: fileID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Marcus Meadow"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Marcus Meadow")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
		BookFileID: fileID,
		AuthorID:   author.ID,
	}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "Könnyek városa" {
		t.Errorf("expected washed book title, got %q", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Marcus Meadow"}) {
		t.Errorf("expected authors untouched, got %q", b.Authors)
	}
	if len(b.Files) != 1 || b.Files[0].Title != "Marcus Meadow - Könnyek városa" {
		t.Errorf("expected file entry to keep original title, got %+v", b.Files)
	}
}

func TestIsTitleEcho(t *testing.T) {
	for _, tc := range []struct {
		name   string
		author string
		title  string
		want   bool
	}{
		{"exact echo", "Patkánykirály", "Patkánykirály", true},
		{"case and accent insensitive", "patkanykiraly", "Patkánykirály", true},
		{"different", "James Clavell", "Patkánykirály", false},
		{"empty title never filters", "Somebody", "", false},
		{"empty author never filters", "", "Something", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTitleEcho(tc.author, tc.title); got != tc.want {
				t.Errorf("isTitleEcho(%q, %q) = %v, want %v", tc.author, tc.title, got, tc.want)
			}
		})
	}
}

func TestParseCalibreAuthorOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     string
		wantAuth string
		wantOK   bool
	}{
		{"two word author", "/books/Patkanykiraly_-_James_Clavell.epub", "James Clavell", true},
		{"comma author", "/books/A romai nep tortenete 1 - Livius, Titus.epub", "Livius, Titus", true},
		{"three word subtitle", "/books/Dune - A sivatag bolygója.epub", "", false},
		{"single tokens", "/books/X - A B.epub", "", false},
		{"no separator", "/books/Dune.epub", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, ok := parseCalibreAuthorOnly(tc.path)
			if auth != tc.wantAuth || ok != tc.wantOK {
				t.Errorf("parseCalibreAuthorOnly(%q) = (%q, %v), want (%q, %v)",
					tc.path, auth, ok, tc.wantAuth, tc.wantOK)
			}
		})
	}
}

func TestWriteBooksEchoAuthorRecovered(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	addEchoBook := func(bookTitle, fileTitle, echo, hash, path string) {
		t.Helper()
		bookID, err := q.CreateBook(ctx, bookTitle)
		if err != nil {
			t.Fatal(err)
		}
		fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
			Hash:  hash,
			Path:  path,
			Title: fileTitle,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
			BookID:     bookID,
			BookFileID: fileID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateAuthor(ctx, echo); err != nil {
			t.Fatal(err)
		}
		author, err := q.GetAuthorByName(ctx, echo)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: fileID,
			AuthorID:   author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	addEchoBook("Patkánykirály", "Patkánykirály", "Patkánykirály",
		"hash-pat", "/home/attila/books/Regények/J/James Clavel/Patkanykiraly_-_James_Clavell.epub")
	addEchoBook("Gajdzsin", "Gajdzsin", "Gajdzsin",
		"hash-gaj", "/home/attila/books/Regények/J/James Clavel/Gajdzsin_-_James_Clavell.epub")

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 2 {
		t.Fatalf("expected 2 books, got %d", len(report))
	}
	for _, b := range report {
		if !reflect.DeepEqual(b.Authors, []string{"James Clavell"}) {
			t.Errorf("expected recovered authors [James Clavell], got %q (title %q)", b.Authors, b.Title)
		}
		if len(b.Files) != 1 || len(b.Files[0].Authors) != 0 {
			t.Errorf("expected file entry with filtered authors, got %+v", b.Files)
		}
	}
	if report[0].Title != "Patkánykirály" || report[1].Title != "Gajdzsin" {
		t.Errorf("expected real titles kept, got %q and %q", report[0].Title, report[1].Title)
	}
}

func TestWriteBooksWashLeavesCleanTitle(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	bookID, err := q.CreateBook(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateAuthor(ctx, "Steven Saylor"); err != nil {
		t.Fatal(err)
	}
	author, err := q.GetAuthorByName(ctx, "Steven Saylor")
	if err != nil {
		t.Fatal(err)
	}
	addFile := func(hash, path, title string) {
		t.Helper()
		fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
			Hash:  hash,
			Path:  path,
			Title: title,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
			BookID:     bookID,
			BookFileID: fileID,
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
			BookFileID: fileID,
			AuthorID:   author.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	addFile("hash-clean-1", "books/glad1.epub", "Egy gladiátor csak egyszer hal meg")
	addFile("hash-clean-2", "books/glad2.mobi", "Egy gladiátor csak egyszer hal meg")
	addFile("hash-clean-3", "books/glad3.epub", "Egy gladiátor csak egyszer hal meg")
	addFile("hash-noisy", "books/glad4.prc", "Steven Saylor - Egy gladiátor csak egyszer hal meg")

	path := filepath.Join(t.TempDir(), "books.json")
	if err := WriteBooks(q, path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 {
		t.Fatalf("expected 1 book, got %d", len(report))
	}
	b := report[0]
	if b.Title != "Egy gladiátor csak egyszer hal meg" {
		t.Errorf("expected clean majority title kept, got %q", b.Title)
	}
	if !reflect.DeepEqual(b.Authors, []string{"Steven Saylor"}) {
		t.Errorf("expected authors untouched, got %q", b.Authors)
	}
	if len(b.Files) != 4 || b.Files[3].Title != "Steven Saylor - Egy gladiátor csak egyszer hal meg" {
		t.Errorf("expected noisy file entry to keep original title, got %+v", b.Files)
	}
}

func TestWriteBooksFilteredKeepsRequestedOrder(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	q := db.New(conn)
	ctx := context.Background()

	addBook := func(title, hash, path string, authors []string) int64 {
		t.Helper()
		bookID, err := q.CreateBook(ctx, title)
		if err != nil {
			t.Fatal(err)
		}
		fileID, err := q.CreateBookFile(ctx, db.CreateBookFileParams{
			Hash:  hash,
			Path:  path,
			Title: title,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.CreateBookBookFile(ctx, db.CreateBookBookFileParams{
			BookID:     bookID,
			BookFileID: fileID,
		}); err != nil {
			t.Fatal(err)
		}
		for _, name := range authors {
			if err := q.CreateAuthor(ctx, name); err != nil {
				t.Fatal(err)
			}
			author, err := q.GetAuthorByName(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			if err := q.CreateBookFileAuthor(ctx, db.CreateBookFileAuthorParams{
				BookFileID: fileID,
				AuthorID:   author.ID,
			}); err != nil {
				t.Fatal(err)
			}
		}
		return bookID
	}

	mobyID := addBook("Moby Dick", "hash1", "books/mobydick.epub", []string{"Herman Melville"})
	duneID := addBook("Dune", "hash2", "books/dune.epub", []string{"Frank Herbert"})

	// Reversed ID order plus an unknown ID: the output must follow the
	// requested order and skip the unknown ID.
	out := filepath.Join(t.TempDir(), "search.json")
	if err := WriteBooksFiltered(q, []int64{duneID, 9999, mobyID, duneID}, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report []BookReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report) != 2 {
		t.Fatalf("expected 2 books, got %d", len(report))
	}
	if report[0].Title != "Dune" || report[1].Title != "Moby Dick" {
		t.Fatalf("expected [Dune Moby Dick] order, got [%s %s]", report[0].Title, report[1].Title)
	}
	if !reflect.DeepEqual(report[0].Authors, []string{"Frank Herbert"}) {
		t.Errorf("expected Dune authors [Frank Herbert], got %q", report[0].Authors)
	}
	if len(report[0].Files) != 1 || report[0].Files[0].Path != "books/dune.epub" {
		t.Errorf("expected Dune file entry, got %+v", report[0].Files)
	}
}

func TestWriteBooksFilteredEmptyWritesNothing(t *testing.T) {
	conn, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	out := filepath.Join(t.TempDir(), "search.json")
	if err := WriteBooksFiltered(db.New(conn), nil, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written, stat err = %v", err)
	}
}
