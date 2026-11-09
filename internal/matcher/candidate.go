package matcher

import (
	"sort"
	"sync"

	"github.com/f0d0r/margaret-cli/internal/identifier"
)

const (
	defaultMaxCandidatesPerBook      = 20
	defaultMaxWorksCandidatesPerBook = 5
	defaultMaxPosting                = 5000
	defaultMinTitleScore             = 0.35
	defaultISBNTitleConfirm          = 0.60
)

// Candidate is one possible OpenLibrary edition for a scanned book.
type Candidate struct {
	BookID       int64
	EditionKey   string
	WorkKey      string
	EditionTitle string
	WorkTitle    string
	AuthorKeys   []string
	AuthorNames  []string

	ISBN         string
	ISBNSource   identifier.Source
	ISBNPriority int

	TitleScore   float64
	AuthorScore  float64
	ContentScore float64
	// ContentTitleHit and ContentAuthorHit are the fractions of the candidate
	// title and author tokens found in the scanned book's leading text.
	ContentTitleHit  float64
	ContentAuthorHit float64
	// OrigTitleHit and OrigAuthorHit record how strongly the scanned metadata
	// itself appears in the content. They are persisted as evidence.
	OrigTitleHit  float64
	OrigAuthorHit float64
	Score         float64

	Methods []string
}

// StreamOptions configures the dump streaming passes.
type StreamOptions struct {
	MaxCandidatesPerBook      int
	MaxWorksCandidatesPerBook int
	MaxPosting                int
	MinTitleScore             float64
	ISBNTitleConfirm          float64
	// TempDir overrides the directory used for the temporary author sidecar
	// in combined-dump mode. An empty value uses the operating system default.
	TempDir string
	// Workers is the number of editions-pass workers. Values <= 1 run the
	// pass in the calling goroutine. The combined-dump extraction pass remains
	// single-threaded because it also writes the author sidecar.
	Workers  int
	Progress func(StreamProgress)
}

func (o StreamOptions) withDefaults() StreamOptions {
	if o.MaxCandidatesPerBook <= 0 {
		o.MaxCandidatesPerBook = defaultMaxCandidatesPerBook
	}
	if o.MaxWorksCandidatesPerBook <= 0 {
		o.MaxWorksCandidatesPerBook = defaultMaxWorksCandidatesPerBook
	}
	if o.MaxPosting <= 0 {
		o.MaxPosting = defaultMaxPosting
	}
	if o.MinTitleScore <= 0 {
		o.MinTitleScore = defaultMinTitleScore
	}
	if o.ISBNTitleConfirm <= 0 {
		o.ISBNTitleConfirm = defaultISBNTitleConfirm
	}
	if o.Workers <= 0 {
		o.Workers = 1
	}
	return o
}

// StreamProgress is emitted periodically while a dump pass runs.
type StreamProgress struct {
	Pass       string
	Lines      int64
	Records    int64
	Candidates int64
	// Total and Done describe the units of the current pass. For editions the
	// unit is a scanned book that can be matched; for authors and works it is
	// a requested key.
	Total   int
	Done    int
	Current string
	// BytesRead and BytesTotal describe progress through the dump file. They
	// are the primary progress metric for editions and combined passes.
	BytesRead  int64
	BytesTotal int64
	// Pending is the number of mappable books without a candidate yet in the
	// editions and combined passes.
	Pending int
}

// StreamStats summarizes a streaming run.
type StreamStats struct {
	Lines       int64
	Editions    int64
	Authors     int64
	Works       int64
	Malformed   int64
	ISBNMatches int64

	TitleExactMatches int64
	TitleFuzzyMatches int64

	Candidates          int64
	BooksWithCandidates int64
}

// AuthorRecord is the matching view of one OpenLibrary author.
type AuthorRecord struct {
	Key   string
	Names []string
}

// WorkRecord is the matching view of one OpenLibrary work.
type WorkRecord struct {
	Key              string
	Title            string
	Description      string
	Subjects         []string
	FirstPublishDate string
	AuthorKeys       []string
}

// StreamResult is the output of the dump streaming step.
type StreamResult struct {
	Candidates map[int64][]Candidate
	Authors    map[string]AuthorRecord
	Works      map[string]WorkRecord
	Stats      StreamStats
	Warnings   []string
}

type candidateSet struct {
	mu     sync.Mutex
	byBook map[int64][]*Candidate
}

func newCandidateSet() *candidateSet {
	return &candidateSet{byBook: make(map[int64][]*Candidate)}
}

func (s *candidateSet) add(candidate Candidate, max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byBook[candidate.BookID]
	for _, existing := range list {
		if existing.EditionKey == candidate.EditionKey {
			mergeCandidate(existing, &candidate)
			return
		}
	}

	candidate.Score = candidate.computeScore()
	if len(list) < max {
		s.byBook[candidate.BookID] = append(list, &candidate)
		return
	}

	worst := 0
	for i := 1; i < len(list); i++ {
		if candidateWorse(*list[i], *list[worst]) {
			worst = i
		}
	}
	if !betterCandidate(candidate, *list[worst]) {
		return
	}
	list[worst] = &candidate
}

func mergeCandidate(existing, next *Candidate) {
	if next.ISBN != "" && (existing.ISBN == "" || next.ISBNPriority < existing.ISBNPriority) {
		existing.ISBN = next.ISBN
		existing.ISBNSource = next.ISBNSource
		existing.ISBNPriority = next.ISBNPriority
	}
	if next.TitleScore > existing.TitleScore {
		existing.TitleScore = next.TitleScore
	}
	if next.WorkKey != "" && existing.WorkKey == "" {
		existing.WorkKey = next.WorkKey
	}
	existing.AuthorKeys = unionStrings(existing.AuthorKeys, next.AuthorKeys)
	existing.Methods = unionStrings(existing.Methods, next.Methods)
	existing.Score = existing.computeScore()
}

func (c *Candidate) computeScore() float64 {
	text := 0.6*c.TitleScore + 0.3*c.AuthorScore + 0.1*c.ContentScore
	if c.ISBN == "" {
		return text
	}
	return 0.7*isbnStrength(c.ISBNPriority) + 0.3*text
}

func isbnStrength(priority int) float64 {
	switch priority {
	case 0:
		return 1.0
	case 1:
		return 0.9
	case 2:
		return 0.75
	default:
		return 0.5
	}
}

func (s *candidateSet) authorKeys() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make(map[string]struct{})
	for _, list := range s.byBook {
		for _, candidate := range list {
			for _, key := range candidate.AuthorKeys {
				keys[key] = struct{}{}
			}
		}
	}
	return keys
}

func (s *candidateSet) workKeys(max int) map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make(map[string]struct{})
	for _, list := range s.byBook {
		for i, candidate := range list {
			if i >= max {
				break
			}
			if candidate.WorkKey != "" {
				keys[candidate.WorkKey] = struct{}{}
			}
		}
	}
	return keys
}

func (s *candidateSet) sortAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, list := range s.byBook {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Score != list[j].Score {
				return list[i].Score > list[j].Score
			}
			if list[i].ISBNPriority != list[j].ISBNPriority {
				return list[i].ISBNPriority < list[j].ISBNPriority
			}
			if list[i].TitleScore != list[j].TitleScore {
				return list[i].TitleScore > list[j].TitleScore
			}
			return list[i].EditionKey < list[j].EditionKey
		})
	}
}

func (s *candidateSet) total() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, list := range s.byBook {
		total += int64(len(list))
	}
	return total
}

func (s *candidateSet) bookCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.byBook))
}

func (s *candidateSet) values() map[int64][]Candidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64][]Candidate, len(s.byBook))
	for bookID, list := range s.byBook {
		items := make([]Candidate, len(list))
		for i, candidate := range list {
			items[i] = *candidate
		}
		out[bookID] = items
	}
	return out
}

func unionStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, values := range [][]string{a, b} {
		for _, value := range values {
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}
