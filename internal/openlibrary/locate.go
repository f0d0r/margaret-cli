// Package openlibrary locates, downloads and validates OpenLibrary's bulk
// data dumps. The dumps are read locally; no OpenLibrary web API is used.
package openlibrary

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Kind identifies one of the OpenLibrary dump files.
type Kind string

const (
	KindEditions Kind = "editions"
	KindWorks    Kind = "works"
	KindAuthors  Kind = "authors"
	KindAll      Kind = "all"
)

// EnvDir overrides the default dump search just like --openlibrary-dir.
const EnvDir = "MARGARET_OPENLIBRARY_DIR"

// DefaultKinds lists the dump families the enrich command uses, in download
// order.
var DefaultKinds = []Kind{KindEditions, KindWorks, KindAuthors}

// File describes one dump file found on disk.
type File struct {
	Kind       Kind      `json:"kind"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Version    string    `json:"version"`
	Modified   time.Time `json:"modified"`
	Compressed bool      `json:"compressed"`
}

// Date returns the dump's nominal date, falling back to the file's mtime for
// files whose names carry no date.
func (f File) Date() time.Time {
	if f.Version != "" && f.Version != "latest" {
		if date, err := time.Parse("2006-01-02", f.Version); err == nil {
			return date
		}
	}
	return f.Modified
}

// InvalidFile records a dump file that exists but failed validation.
type InvalidFile struct {
	Path string
	Err  error
}

// Dataset is the set of dump files found in one directory. Editions, Works
// and Authors may point at the combined all-types dump when the dedicated
// family file is missing.
type Dataset struct {
	Dir      string
	Editions *File
	Works    *File
	Authors  *File
	Combined *File
	Warnings []string
	Invalid  []InvalidFile
}

// Usable reports whether the dataset can be used for matching.
func (d *Dataset) Usable() bool {
	return d != nil && d.Editions != nil
}

// Files returns the distinct files backing the dataset.
func (d *Dataset) Files() []File {
	if d == nil {
		return nil
	}
	seen := make(map[string]bool)
	var files []File
	for _, file := range []*File{d.Editions, d.Works, d.Authors, d.Combined} {
		if file == nil || seen[file.Path] {
			continue
		}
		seen[file.Path] = true
		files = append(files, *file)
	}
	return files
}

// UsesCombined reports whether any dump family is backed by the combined
// all-types dump.
func (d *Dataset) UsesCombined() bool {
	if d == nil || d.Combined == nil {
		return false
	}
	for _, file := range []*File{d.Editions, d.Works, d.Authors} {
		if file != nil && file.Path == d.Combined.Path {
			return true
		}
	}
	return false
}

// VersionDate returns the date used to compare datasets from different
// directories.
func (d *Dataset) VersionDate() time.Time {
	if d == nil {
		return time.Time{}
	}
	if d.Editions != nil {
		return d.Editions.Date()
	}
	if d.Combined != nil {
		return d.Combined.Date()
	}
	return time.Time{}
}

// Candidate is one directory in the dump search.
type Candidate struct {
	Dir     string
	Origin  string
	Dataset *Dataset
	Err     error
}

// Usable reports whether the candidate holds a usable dataset.
func (c Candidate) Usable() bool {
	return c.Err == nil && c.Dataset.Usable()
}

// NotFoundError reports that no usable dump was found.
type NotFoundError struct {
	Candidates []Candidate
}

func (e *NotFoundError) Error() string { return "no OpenLibrary dump found" }

// Candidates returns the directories to search. An explicit --openlibrary-dir
// or MARGARET_OPENLIBRARY_DIR disables the default search.
func Candidates(explicitDir string) []Candidate {
	if explicitDir != "" {
		return []Candidate{loadCandidate(explicitDir, "flag")}
	}
	if env := strings.TrimSpace(os.Getenv(EnvDir)); env != "" {
		return []Candidate{loadCandidate(env, "env")}
	}

	var candidates []Candidate
	seen := make(map[string]bool)
	add := func(dir, origin string) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if seen[dir] {
			return
		}
		seen[dir] = true
		candidates = append(candidates, loadCandidate(dir, origin))
	}

	add(filepath.Join("data", "openlibrary"), "cwd")
	if dir, err := DataDir(); err == nil {
		add(dir, "xdg")
	}
	if dir, err := executableDataDir(); err == nil {
		add(dir, "exe")
	}
	return candidates
}

// Select returns the usable candidate with the newest dataset.
func Select(candidates []Candidate) (*Candidate, error) {
	best := -1
	for i := range candidates {
		candidate := &candidates[i]
		if !candidate.Usable() {
			continue
		}
		if best < 0 || candidate.Dataset.VersionDate().After(candidates[best].Dataset.VersionDate()) {
			best = i
		}
	}
	if best < 0 {
		return nil, &NotFoundError{Candidates: candidates}
	}
	return &candidates[best], nil
}

// Resolve returns the newest usable dataset, or a NotFoundError.
func Resolve(explicitDir string) (*Dataset, error) {
	candidate, err := Select(Candidates(explicitDir))
	if err != nil {
		return nil, err
	}
	return candidate.Dataset, nil
}

// LoadDataset reads dir and returns every recognised dump file, selecting the
// newest valid file per family. A directory with no dumps yields an unusable
// dataset rather than an error.
func LoadDataset(dir string) (*Dataset, error) {
	files, err := scanDir(dir)
	if err != nil {
		return nil, err
	}

	dataset := &Dataset{Dir: dir}
	pick := func(kind Kind) *File {
		file, bad := newestValid(files[kind])
		dataset.Invalid = append(dataset.Invalid, bad...)
		return file
	}
	dataset.Editions = pick(KindEditions)
	dataset.Works = pick(KindWorks)
	dataset.Authors = pick(KindAuthors)
	dataset.Combined = pick(KindAll)

	if dataset.Editions == nil && dataset.Combined != nil {
		dataset.Editions = dataset.Combined
	}
	if dataset.Works == nil && dataset.Combined != nil {
		dataset.Works = dataset.Combined
		dataset.Warnings = append(dataset.Warnings, "works will be read from the combined dump")
	}
	if dataset.Authors == nil && dataset.Combined != nil {
		dataset.Authors = dataset.Combined
		dataset.Warnings = append(dataset.Warnings, "authors will be read from the combined dump")
	}

	dataset.addWarnings()
	return dataset, nil
}

func (d *Dataset) addWarnings() {
	if d.Editions == nil {
		d.Warnings = append(d.Warnings, "no editions or combined dump found")
	}
	if d.Works == nil {
		d.Warnings = append(d.Warnings, "no works dump found; work-level metadata will not be available")
	}
	if d.Authors == nil {
		d.Warnings = append(d.Warnings, "no authors dump found; author names cannot be resolved")
	}

	versions := make(map[string]struct{})
	for _, file := range []*File{d.Editions, d.Works, d.Authors} {
		if file == nil || file.Version == "" || file.Version == "latest" {
			continue
		}
		versions[file.Version] = struct{}{}
	}
	if len(versions) > 1 {
		var names []string
		for version := range versions {
			names = append(names, version)
		}
		sort.Strings(names)
		d.Warnings = append(d.Warnings,
			fmt.Sprintf("dump files have different versions: %s", strings.Join(names, ", ")))
	}
}

// Missing returns the families that are not present in dir. A combined dump
// satisfies all three.
func Missing(dir string) []Kind {
	dataset, err := LoadDataset(dir)
	if err != nil || dataset == nil {
		return append([]Kind(nil), DefaultKinds...)
	}
	var missing []Kind
	if dataset.Editions == nil {
		missing = append(missing, KindEditions)
	}
	if dataset.Works == nil {
		missing = append(missing, KindWorks)
	}
	if dataset.Authors == nil {
		missing = append(missing, KindAuthors)
	}
	return missing
}

// DownloadDir chooses where downloaded dumps belong. An explicit directory or
// env var wins; otherwise an existing default directory is reused before
// falling back to the per-user data directory.
func DownloadDir(explicitDir string) (string, error) {
	if explicitDir != "" {
		return explicitDir, nil
	}
	if env := strings.TrimSpace(os.Getenv(EnvDir)); env != "" {
		return env, nil
	}

	candidates := Candidates("")
	for _, candidate := range candidates {
		if candidate.Dataset != nil && len(candidate.Dataset.Files()) > 0 {
			return candidate.Dir, nil
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate.Dir); err == nil && info.IsDir() {
			return candidate.Dir, nil
		}
	}
	return DataDir()
}

// DataDir returns the per-user directory for reusable OpenLibrary data.
func DataDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(dir, "margaret", "openlibrary"), nil
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.Getenv("APPDATA")
		}
		if base == "" {
			return "", errors.New("cannot determine the data directory: LOCALAPPDATA and APPDATA are unset")
		}
		return filepath.Join(base, "margaret", "openlibrary"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "margaret", "openlibrary"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", "margaret", "openlibrary"), nil
	}
}

func executableDataDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Join(filepath.Dir(executable), "data", "openlibrary"), nil
}

func loadCandidate(dir, origin string) Candidate {
	dataset, err := LoadDataset(dir)
	return Candidate{Dir: dir, Origin: origin, Dataset: dataset, Err: err}
}

func scanDir(dir string) (map[Kind][]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make(map[Kind][]File)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		kind, version, compressed, ok := parseDumpName(entry.Name())
		if !ok {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		files[kind] = append(files[kind], File{
			Kind:       kind,
			Path:       path,
			Size:       info.Size(),
			Version:    version,
			Modified:   info.ModTime(),
			Compressed: compressed,
		})
	}
	for kind := range files {
		sortNewestFirst(files[kind])
	}
	return files, nil
}

func newestValid(files []File) (*File, []InvalidFile) {
	var invalid []InvalidFile
	for _, file := range files {
		if err := validateDump(file.Path, file.Compressed); err != nil {
			invalid = append(invalid, InvalidFile{Path: file.Path, Err: err})
			continue
		}
		valid := file
		return &valid, invalid
	}
	return nil, invalid
}

func sortNewestFirst(files []File) {
	sort.SliceStable(files, func(i, j int) bool {
		left, right := files[i].Date(), files[j].Date()
		if !left.Equal(right) {
			return left.After(right)
		}
		return files[i].Path > files[j].Path
	})
}

// parseDumpName recognises the published dump names, either dated or _latest,
// with or without gzip compression.
func parseDumpName(name string) (Kind, string, bool, bool) {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".part") {
		return "", "", false, false
	}

	compressed := false
	switch {
	case strings.HasSuffix(lower, ".txt.gz"):
		compressed = true
		lower = strings.TrimSuffix(lower, ".txt.gz")
	case strings.HasSuffix(lower, ".txt"):
		lower = strings.TrimSuffix(lower, ".txt")
	default:
		return "", "", false, false
	}

	if !strings.HasPrefix(lower, "ol_dump_") {
		return "", "", false, false
	}
	rest := strings.TrimPrefix(lower, "ol_dump_")

	kind := KindAll
	for _, family := range []Kind{KindEditions, KindWorks, KindAuthors} {
		prefix := string(family) + "_"
		if strings.HasPrefix(rest, prefix) {
			kind = family
			rest = strings.TrimPrefix(rest, prefix)
			break
		}
	}

	if rest == "latest" {
		return kind, "latest", compressed, true
	}
	if _, err := time.Parse("2006-01-02", rest); err == nil {
		return kind, rest, compressed, true
	}
	return "", "", false, false
}

func validateDump(path string, compressed bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	var reader io.Reader = file
	if compressed {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("not a gzip file: %w", err)
		}
		defer func() {
			_ = gzipReader.Close()
		}()
		reader = gzipReader
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(reader, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if n == 0 {
		return errors.New("empty dump")
	}
	if !bytes.Contains(head[:n], []byte("/type/")) {
		return errors.New("not an OpenLibrary dump (missing /type/ column)")
	}
	return nil
}
