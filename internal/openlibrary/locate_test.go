package openlibrary

import (
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeGzipDump(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write([]byte("/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writePlainDump(t *testing.T, path string) {
	t.Helper()
	body := "/type/edition\t/books/OL1M\t1\t2024-01-01\t{\"title\":\"Test\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseDumpName(t *testing.T) {
	tests := []struct {
		name       string
		kind       Kind
		version    string
		compressed bool
		ok         bool
	}{
		{"ol_dump_editions_latest.txt.gz", KindEditions, "latest", true, true},
		{"ol_dump_editions_2024-01-31.txt.gz", KindEditions, "2024-01-31", true, true},
		{"ol_dump_works_2024-01-31.txt", KindWorks, "2024-01-31", false, true},
		{"ol_dump_authors_latest.txt.gz", KindAuthors, "latest", true, true},
		{"ol_dump_latest.txt.gz", KindAll, "latest", true, true},
		{"ol_dump_2024-01-31.txt.gz", KindAll, "2024-01-31", true, true},
		{"ol_dump_editions_2024-01-31.txt.gz.part", "", "", false, false},
		{"random.txt.gz", "", "", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, version, compressed, ok := parseDumpName(test.name)
			if kind != test.kind || version != test.version || compressed != test.compressed || ok != test.ok {
				t.Fatalf("parseDumpName(%q) = (%q, %q, %v, %v), want (%q, %q, %v, %v)",
					test.name, kind, version, compressed, ok, test.kind, test.version, test.compressed, test.ok)
			}
		})
	}
}

func TestLoadDatasetSelectsNewestValid(t *testing.T) {
	dir := t.TempDir()
	writeGzipDump(t, filepath.Join(dir, "ol_dump_editions_2024-01-31.txt.gz"))
	writeGzipDump(t, filepath.Join(dir, "ol_dump_editions_2024-02-29.txt.gz"))
	writeGzipDump(t, filepath.Join(dir, "ol_dump_works_2024-02-29.txt.gz"))
	writeGzipDump(t, filepath.Join(dir, "ol_dump_authors_2024-02-29.txt.gz"))

	dataset, err := LoadDataset(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dataset.Usable() {
		t.Fatal("dataset should be usable")
	}
	if got := dataset.Editions.Version; got != "2024-02-29" {
		t.Fatalf("editions version = %q, want 2024-02-29", got)
	}
	if dataset.Works == nil || dataset.Authors == nil {
		t.Fatal("works and authors should be present")
	}
	if len(dataset.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", dataset.Warnings)
	}
}

func TestLoadDatasetSkipsInvalidNewest(t *testing.T) {
	dir := t.TempDir()
	writeGzipDump(t, filepath.Join(dir, "ol_dump_editions_2024-01-31.txt.gz"))
	if err := os.WriteFile(filepath.Join(dir, "ol_dump_editions_2024-02-29.txt.gz"), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	dataset, err := LoadDataset(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dataset.Editions.Version; got != "2024-01-31" {
		t.Fatalf("editions version = %q, want the older valid file", got)
	}
	if len(dataset.Invalid) != 1 {
		t.Fatalf("invalid files = %d, want 1", len(dataset.Invalid))
	}
}

func TestLoadDatasetCombinedFallback(t *testing.T) {
	dir := t.TempDir()
	writeGzipDump(t, filepath.Join(dir, "ol_dump_2024-02-29.txt.gz"))

	dataset, err := LoadDataset(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dataset.Usable() {
		t.Fatal("combined dump should be usable")
	}
	if dataset.Works == nil || dataset.Authors == nil {
		t.Fatal("works and authors should fall back to the combined dump")
	}
	if got := len(dataset.Files()); got != 1 {
		t.Fatalf("files = %d, want 1", got)
	}
	if len(dataset.Warnings) != 2 {
		t.Fatalf("warnings = %v, want two combined-dump warnings", dataset.Warnings)
	}
}

func TestLoadDatasetMissingFamilies(t *testing.T) {
	dir := t.TempDir()
	writePlainDump(t, filepath.Join(dir, "ol_dump_editions_2024-01-31.txt"))

	dataset, err := LoadDataset(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Editions == nil {
		t.Fatal("editions should be present")
	}
	if dataset.Works != nil || dataset.Authors != nil {
		t.Fatal("works and authors should be missing")
	}
	if len(dataset.Warnings) != 2 {
		t.Fatalf("warnings = %v, want two missing-family warnings", dataset.Warnings)
	}
}

func TestCandidatesExplicitOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, filepath.Join(dir, "env"))

	candidates := Candidates(dir)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Dir != dir || candidates[0].Origin != "flag" {
		t.Fatalf("candidate = %+v", candidates[0])
	}
}

func TestCandidatesEnvOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	candidates := Candidates("")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Dir != dir || candidates[0].Origin != "env" {
		t.Fatalf("candidate = %+v", candidates[0])
	}
}

func TestCandidatesDefaultSearch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(EnvDir, "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "xdg"))

	candidates := Candidates("")
	if len(candidates) < 2 {
		t.Fatalf("candidates = %d, want at least cwd and xdg", len(candidates))
	}
	if candidates[0].Origin != "cwd" {
		t.Fatalf("first origin = %q, want cwd", candidates[0].Origin)
	}
	wantXDG := filepath.Join(dir, "xdg", "margaret", "openlibrary")
	foundXDG := false
	for _, candidate := range candidates {
		if candidate.Origin == "xdg" {
			foundXDG = candidate.Dir == wantXDG
		}
	}
	if !foundXDG {
		t.Fatalf("xdg candidate = %+v, want %s", candidates, wantXDG)
	}
}

func TestSelectNewest(t *testing.T) {
	oldDir := t.TempDir()
	writeGzipDump(t, filepath.Join(oldDir, "ol_dump_editions_2024-01-31.txt.gz"))
	newDir := t.TempDir()
	writeGzipDump(t, filepath.Join(newDir, "ol_dump_editions_2024-02-29.txt.gz"))

	candidates := []Candidate{
		loadCandidate(oldDir, "cwd"),
		loadCandidate(newDir, "xdg"),
	}
	selected, err := Select(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Dir != newDir {
		t.Fatalf("selected = %q, want %q", selected.Dir, newDir)
	}
}

func TestSelectNotFound(t *testing.T) {
	candidates := []Candidate{loadCandidate(filepath.Join(t.TempDir(), "missing"), "cwd")}
	_, err := Select(candidates)
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want a NotFoundError", err)
	}
	if len(notFound.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(notFound.Candidates))
	}
}

func TestDownloadDir(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		dir := t.TempDir()
		got, err := DownloadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Fatalf("DownloadDir = %q, want %q", got, dir)
		}
	})

	t.Run("env", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(EnvDir, dir)
		got, err := DownloadDir("")
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Fatalf("DownloadDir = %q, want %q", got, dir)
		}
	})

	t.Run("existing cwd directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		t.Setenv(EnvDir, "")
		t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "xdg"))
		cwdData := filepath.Join(dir, "data", "openlibrary")
		if err := os.MkdirAll(cwdData, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := DownloadDir("")
		if err != nil {
			t.Fatal(err)
		}
		if got != cwdData {
			t.Fatalf("DownloadDir = %q, want %q", got, cwdData)
		}
	})

	t.Run("xdg fallback", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		t.Setenv(EnvDir, "")
		xdg := filepath.Join(dir, "xdg")
		t.Setenv("XDG_DATA_HOME", xdg)
		got, err := DownloadDir("")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(xdg, "margaret", "openlibrary")
		if got != want {
			t.Fatalf("DownloadDir = %q, want %q", got, want)
		}
	})
}

func TestMissing(t *testing.T) {
	dir := t.TempDir()
	writeGzipDump(t, filepath.Join(dir, "ol_dump_editions_2024-01-31.txt.gz"))

	missing := Missing(dir)
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want works and authors", missing)
	}
	if missing[0] != KindWorks || missing[1] != KindAuthors {
		t.Fatalf("missing = %v, want [works authors]", missing)
	}
}

func TestFormatSize(t *testing.T) {
	tests := map[int64]string{
		0:             "0 B",
		999:           "999 B",
		1000:          "1.0 kB",
		9_200_000_000: "9.2 GB",
	}
	for input, want := range tests {
		if got := FormatSize(input); got != want {
			t.Fatalf("FormatSize(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestParseDumpNameRejectsBadDate(t *testing.T) {
	if _, _, _, ok := parseDumpName("ol_dump_editions_2024-99-99.txt.gz"); ok {
		t.Fatal("invalid date should not be recognised")
	}
}

func TestContentRangeTotal(t *testing.T) {
	if got := contentRangeTotal("bytes 0-0/1234"); got != 1234 {
		t.Fatalf("contentRangeTotal = %d, want 1234", got)
	}
	if got := contentRangeTotal("bytes */1234"); got != 1234 {
		t.Fatalf("contentRangeTotal = %d, want 1234", got)
	}
	if got := contentRangeTotal("bad"); got != 0 {
		t.Fatalf("contentRangeTotal = %d, want 0", got)
	}
}

func TestApproximateTotal(t *testing.T) {
	if got, want := ApproximateTotal(DefaultKinds), int64(12_600_000_000); got != want {
		t.Fatalf("ApproximateTotal = %s, want %s", strconv.FormatInt(got, 10), strconv.FormatInt(want, 10))
	}
}
