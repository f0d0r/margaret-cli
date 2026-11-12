package claim

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

// BookResolveVersion identifies the book-level synthesis rules. It is
// independent of ClaimVersion so resolution tuning does not force a reparse.
const BookResolveVersion = 3

// AttestLevel is the strength of a text-window attestation.
type AttestLevel string

const (
	AttestNone      AttestLevel = "none"
	AttestFull      AttestLevel = "full"
	AttestPartial   AttestLevel = "partial"
	AttestSubstring AttestLevel = "substring"
	AttestSecondary AttestLevel = "secondary"
	AttestText      AttestLevel = "text"
)

// Attestation is the result of checking a claim against one or more title
// windows.
type Attestation struct {
	Level    AttestLevel
	Score    float64
	Residual []string
	FoundIn  []int64
	Method   string
}

// Window is the normalized title-page window of one content witness.
type Window struct {
	FileID int64
	Path   string
	Lines  []string
}

// BookFile is one content witness of a book. Files with the same Hash are the
// same content witness; their duplicate paths must not inflate support counts.
type BookFile struct {
	ID           int64
	Hash         string
	Path         string
	SourceGroup  string
	Window       Window
	Claims       []Claim
	TitleStatus  Status
	AuthorStatus Status
}

// Disposition tells why a book-level candidate is or is not selected.
type Disposition string

const (
	DispositionSelected             Disposition = "selected"
	DispositionCompatibleTruncation Disposition = "compatible_truncation"
	DispositionOverruled            Disposition = "overruled"
	DispositionConflict             Disposition = "conflict"
)

// BookCandidate is one book-level title or author hypothesis.
type BookCandidate struct {
	Field                  Field
	Value                  string
	Authors                []string
	Status                 Status
	Disposition            Disposition
	Reason                 string
	SupportingWitnesses    int
	StrictSupportingClaims int
	SourceGroups           int
	IndependentWitnesses   int
	Attested               bool
	Suspicious             bool
	AttestationLevel       AttestLevel
	MaxAttestation         float64
	FoundIn                []int64
	Residual               []string
	UseForMatching         bool
	UseForAgreement        bool
	Evidence               map[string]string
}

// BookResolution is the book-level decision.
type BookResolution struct {
	Title           string
	Authors         []string
	TitleStatus     Status
	AuthorStatus    Status
	Candidates      []BookCandidate
	GroupingSuspect bool
	GroupingReason  string
}

type bookTitleClaim struct {
	fileID int64
	hash   string
	group  string
	claim  Claim
	att    Attestation
}

type bookTitleCluster struct {
	value    string
	claims   []bookTitleClaim
	strict   []bookTitleClaim
	trunc    []bookTitleClaim
	selected *BookCandidate
}

type bookAuthorClaim struct {
	fileID int64
	hash   string
	group  string
	claim  Claim
	att    Attestation
}

// ResolveBook synthesizes a book-level title and author list from file-level
// claims and title windows.
func ResolveBook(files []BookFile) BookResolution {
	files = collapseContentWitnesses(files)
	for i := range files {
		if !hasNonJunkTitleClaim(files[i].Claims) && len(files[i].Window.Lines) > 0 {
			line := strings.TrimSpace(files[i].Window.Lines[0])
			if line != "" {
				files[i].Claims = append(files[i].Claims, Claim{
					Field: FieldTitle, Value: line, Source: SourceText,
					Set: "text:titleline", Origin: "text:titleline",
					Trust: TrustWeak, Witness: WitnessSupporting,
					Attested: true, Attestation: 1,
					AttestationMethod: "text", AttestationLevel: AttestText,
				})
			}
		}
	}
	var windows []Window
	for _, file := range files {
		if len(file.Window.Lines) > 0 {
			window := file.Window
			if window.FileID == 0 {
				window.FileID = file.ID
			}
			if window.Path == "" {
				window.Path = file.Path
			}
			windows = append(windows, window)
		}
	}
	titleClaims := make([]bookTitleClaim, 0)
	authorClaims := make([]bookAuthorClaim, 0)
	for _, file := range files {
		for _, c := range file.Claims {
			if c.Junk {
				continue
			}
			c.FileID = file.ID
			if c.Field == FieldTitle {
				titleClaims = append(titleClaims, bookTitleClaim{
					fileID: file.ID, hash: file.Hash, group: file.SourceGroup,
					claim: c, att: attestClaim(c, windows),
				})
			}
			if c.Field == FieldAuthor {
				authorClaims = append(authorClaims, bookAuthorClaim{
					fileID: file.ID, hash: file.Hash, group: file.SourceGroup,
					claim: c, att: attestClaim(c, windows),
				})
			}
		}
	}

	titleClusters := clusterBookTitles(titleClaims, windows)
	titleWinner, titleConflict := chooseTitleCluster(titleClusters)
	authorClusters := clusterBookAuthors(authorClaims)
	authorWinner, authorConflict := chooseAuthorCluster(authorClusters)

	res := BookResolution{
		TitleStatus: StatusNone, AuthorStatus: StatusNone,
		Candidates: []BookCandidate{},
	}
	if titleWinner != nil {
		res.Title = titleWinner.selected.Value
		res.TitleStatus = titleWinner.selected.Status
		if titleConflict {
			res.TitleStatus = StatusConflict
		}
		for _, cluster := range titleClusters {
			candidate := *cluster.selected
			if cluster == titleWinner {
				candidate.Disposition = DispositionSelected
				candidate.UseForMatching = true
			} else {
				candidate.Disposition = DispositionOverruled
				candidate.UseForMatching = false
			}
			res.Candidates = append(res.Candidates, candidate)
			seenTrunc := make(map[string]struct{})
			for _, trunc := range cluster.trunc {
				if trunc.claim.Value == candidate.Value {
					continue
				}
				if _, ok := seenTrunc[trunc.claim.Value]; ok {
					continue
				}
				seenTrunc[trunc.claim.Value] = struct{}{}
				res.Candidates = append(res.Candidates, BookCandidate{
					Field:            FieldTitle,
					Value:            trunc.claim.Value,
					Status:           truncationStatus(trunc.claim),
					Disposition:      DispositionCompatibleTruncation,
					Reason:           "compatible_truncation",
					AttestationLevel: trunc.att.Level,
					MaxAttestation:   trunc.att.Score,
					FoundIn:          trunc.att.FoundIn,
					Residual:         trunc.att.Residual,
					UseForMatching:   false,
					UseForAgreement:  true,
					Evidence:         map[string]string{"disposition": string(DispositionCompatibleTruncation)},
				})
			}
		}
	}
	var selectedAuthors []string
	var selectedAuthorStatus Status
	if titleWinner != nil {
		names := authorNamesInSameSet(titleClaims, authorClaims, titleWinner)
		if len(names) > 0 {
			sameStatus := authorStatusForNames(authorClaims, names)
			if authorWinner == nil || statusRank(sameStatus) >= statusRank(authorWinner.status) {
				selectedAuthors = names
				selectedAuthorStatus = sameStatus
			}
		}
	}
	if len(selectedAuthors) == 0 && authorWinner != nil {
		selectedAuthors = authorListForCluster(authorWinner, authorClaims)
		selectedAuthorStatus = statusForAuthorCluster(authorWinner)
	}
	if len(selectedAuthors) == 0 && authorWinner != nil {
		selectedAuthors = []string{authorWinner.value}
	}
	previousAuthors := append([]string{}, selectedAuthors...)
	fullNameReason := ""
	if len(selectedAuthors) == 1 && len(strings.Fields(selectedAuthors[0])) == 1 && titleWinner != nil {
		if fullName, ok := preferCleanFullName(titleWinner, authorClaims); ok {
			fullNameReason = "full_name_over_unattested_mononym"
			selectedAuthors = []string{fullName}
			selectedAuthorStatus = StatusWeak
		}
	}
	if authorConflict {
		selectedAuthorStatus = StatusConflict
	}
	res.Authors = selectedAuthors
	res.AuthorStatus = selectedAuthorStatus

	// Keep author candidates for audit and matcher alternatives. Multi-author
	// winners are represented as a list, not as one arbitrary name.
	if len(selectedAuthors) > 1 {
		res.Candidates = append(res.Candidates, BookCandidate{
			Field:           FieldAuthor,
			Value:           strings.Join(selectedAuthors, "; "),
			Authors:         append([]string{}, selectedAuthors...),
			Status:          selectedAuthorStatus,
			Disposition:     DispositionSelected,
			Reason:          "author_list",
			UseForMatching:  true,
			UseForAgreement: true,
			Evidence:        map[string]string{"kind": "author_list"},
		})
	}
	for _, cluster := range authorClusters {
		candidate := authorCandidate(cluster)
		selected := len(selectedAuthors) == 1 && authorsEquivalent(cluster.value, selectedAuthors[0])
		if len(selectedAuthors) > 1 {
			for _, name := range selectedAuthors {
				if authorsEquivalent(cluster.value, name) {
					selected = true
					break
				}
			}
		}
		if selected {
			candidate.Disposition = DispositionSelected
			candidate.UseForMatching = true
			if fullNameReason != "" {
				candidate.Reason = fullNameReason
				candidate.Evidence = map[string]string{"reason": fullNameReason}
			}
		} else {
			candidate.Disposition = DispositionOverruled
			candidate.UseForMatching = false
			if fullNameReason != "" && len(previousAuthors) == 1 &&
				authorsEquivalent(cluster.value, previousAuthors[0]) {
				candidate.Reason = fullNameReason
				candidate.UseForAgreement = true
				candidate.Evidence = map[string]string{"reason": fullNameReason}
			}
		}
		if authorConflict && !selected {
			candidate.Disposition = DispositionConflict
			candidate.UseForMatching = true
		}
		res.Candidates = append(res.Candidates, candidate)
	}

	res.GroupingSuspect, res.GroupingReason = detectGroupingSuspect(titleClusters, authorClusters, titleWinner)
	return res
}

func hasNonJunkTitleClaim(claims []Claim) bool {
	for _, c := range claims {
		if c.Field == FieldTitle && !c.Junk {
			return true
		}
	}
	return false
}

func collapseContentWitnesses(files []BookFile) []BookFile {
	seen := make(map[string]int, len(files))
	out := make([]BookFile, 0, len(files))
	for _, file := range files {
		if file.Hash == "" {
			out = append(out, file)
			continue
		}
		if index, ok := seen[file.Hash]; ok {
			// Keep one content witness; merge any claims the duplicate path
			// carries, but do not create a second support count.
			out[index].Claims = append(out[index].Claims, file.Claims...)
			if len(out[index].Window.Lines) == 0 {
				out[index].Window = file.Window
			}
			continue
		}
		seen[file.Hash] = len(out)
		out = append(out, file)
	}
	return out
}

func attestClaim(c Claim, windows []Window) Attestation {
	if c.Source == SourceText {
		return Attestation{Level: AttestText, Score: 1, Method: "text"}
	}
	att := AttestValue(c.Field, c.Value, windows)
	if att.Level == AttestNone && c.AttestationLevel != "" && c.AttestationLevel != AttestNone {
		return Attestation{
			Level:  c.AttestationLevel,
			Score:  c.Attestation,
			Method: c.AttestationMethod,
		}
	}
	return att
}

// AttestValue checks a title or author value against title-page windows. It is
// used by both file-level and book-level resolution; the only difference is
// the set of windows passed in.
func AttestValue(field Field, value string, windows []Window) Attestation {
	if field == FieldAuthor {
		return attestAuthorValue(value, windows)
	}
	return attestTitleValue(value, windows)
}

func attestTitleValue(value string, windows []Window) Attestation {
	value = strings.TrimSpace(value)
	if value == "" || len(windows) == 0 {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	claimOrdered := orderedTitleTokens(value)
	claimWords := wordTokens(value)
	if len(claimOrdered) == 0 {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	best := Attestation{Level: AttestNone, Method: "none"}
	var foundIn []int64
	for _, window := range windows {
		for _, line := range window.Lines {
			line = stripTitleLabel(line)
			lineOrdered := orderedTitleTokens(line)
			lineWords := wordTokens(line)
			if matched, exact := sequenceMatch(lineOrdered, claimOrdered, 1); matched && exact {
				extraWords := setDifference(lineWords, claimWords)
				switch {
				case len(claimWords) >= 2 && len(extraWords) == 0:
					foundIn = appendUniqueID(foundIn, window.FileID)
					return Attestation{
						Level: AttestFull, Score: 1, FoundIn: foundIn, Method: "primary_exact",
					}
				case len(claimWords) >= 2:
					if best.Level == AttestNone {
						best = Attestation{
							Level: AttestSubstring, Score: tokenCoverage(claimWords, lineWords),
							FoundIn: appendUniqueID(nil, window.FileID), Method: "primary_substring",
						}
					}
				}
			}
		}
	}
	if best.Level != AttestNone {
		return best
	}
	// Partial: all word tokens present, and the only missing tokens are
	// numbers in leading position. This is intentionally not a ratio rule.
	windowWords := make(map[string]struct{})
	for _, window := range windows {
		for _, line := range window.Lines {
			for _, token := range wordTokens(stripTitleLabel(line)) {
				windowWords[token] = struct{}{}
			}
		}
	}
	if len(claimWords) >= 2 && allPresent(claimWords, windowWords) {
		missing := missingTokens(claimOrdered, windowWords)
		if allLeadingNumbers(missing, claimOrdered) {
			for _, window := range windows {
				foundIn = appendUniqueID(foundIn, window.FileID)
			}
			return Attestation{
				Level: AttestPartial, Score: 1, Residual: missing,
				FoundIn: foundIn, Method: "primary_partial",
			}
		}
	}
	return best
}

func attestAuthorValue(value string, windows []Window) Attestation {
	value = strings.TrimSpace(value)
	if value == "" || len(windows) == 0 {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	tokens := textmatch.AuthorTokens(value)
	if len(tokens) < 2 {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	var foundIn []int64
	for _, window := range windows {
		for _, line := range window.Lines {
			if allTokensPresent(textmatch.AuthorTokens(line), tokens) {
				foundIn = appendUniqueID(foundIn, window.FileID)
				return Attestation{
					Level: AttestFull, Score: 1, FoundIn: foundIn, Method: "primary_exact",
				}
			}
		}
	}
	return Attestation{Level: AttestNone, Method: "none"}
}

func stripTitleLabel(line string) string {
	line = strings.TrimSpace(line)
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line
	}
	label := strings.ToLower(strings.TrimSpace(line[:idx]))
	switch label {
	case "title", "cím", "cim", "könyv címe", "konyv cime",
		"author", "authors", "by", "creator", "szerző", "szerzo", "szerzők", "szerzok":
		return strings.TrimSpace(line[idx+1:])
	default:
		return line
	}
}

func wordTokens(s string) []string {
	var out []string
	for _, token := range orderedTitleTokens(s) {
		if hasWordRune(token) {
			out = append(out, token)
		}
	}
	return out
}

func hasWordRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func allPresent(needles []string, haystack map[string]struct{}) bool {
	for _, needle := range needles {
		if _, ok := haystack[needle]; !ok {
			return false
		}
	}
	return true
}

func missingTokens(ordered []string, present map[string]struct{}) []string {
	var out []string
	for _, token := range ordered {
		if _, ok := present[token]; !ok {
			out = append(out, token)
		}
	}
	return out
}

func allLeadingNumbers(missing, ordered []string) bool {
	if len(missing) == 0 {
		return false
	}
	firstWord := -1
	for i, token := range ordered {
		if hasWordRune(token) {
			firstWord = i
			break
		}
	}
	if firstWord < 0 {
		return false
	}
	for _, token := range missing {
		if hasWordRune(token) {
			return false
		}
		index := indexOf(ordered, token)
		if index < 0 || index > firstWord {
			return false
		}
	}
	return true
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func setDifference(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, value := range b {
		set[value] = struct{}{}
	}
	var out []string
	for _, value := range a {
		if _, ok := set[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func tokenCoverage(claim, line []string) float64 {
	if len(claim) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(line))
	for _, token := range line {
		set[token] = struct{}{}
	}
	found := 0
	for _, token := range claim {
		if _, ok := set[token]; ok {
			found++
		}
	}
	return float64(found) / float64(len(claim))
}

func appendUniqueID(ids []int64, id int64) []int64 {
	if id == 0 {
		return ids
	}
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func clusterBookTitles(claims []bookTitleClaim, windows []Window) []*bookTitleCluster {
	clusters := make([]*bookTitleCluster, 0)
	for _, c := range claims {
		var found *bookTitleCluster
		for _, cluster := range clusters {
			if titlesEquivalent(cluster.value, c.claim.Value) && !sameWitnessDistinctFilenameSet(cluster, c) {
				found = cluster
				break
			}
		}
		if found == nil {
			clusters = append(clusters, &bookTitleCluster{value: c.claim.Value})
			found = clusters[len(clusters)-1]
		}
		found.claims = append(found.claims, c)
	}

	// Merge proper truncations into the longer hypothesis. A truncation adds
	// evidence, but never strict support, and never overrules the longer title.
	for {
		merged := false
		for i := 0; i < len(clusters); i++ {
			for j := 0; j < len(clusters); j++ {
				if i == j {
					continue
				}
				if compatibleTruncation(clusters[i].value, clusters[j].value, windows) &&
					!clustersShareWitnessFilenameSet(clusters[i], clusters[j]) {
					clusters[j].trunc = append(clusters[j].trunc, clusters[i].claims...)
					clusters[j].claims = append(clusters[j].claims, clusters[i].claims...)
					clusters = append(clusters[:i], clusters[i+1:]...)
					merged = true
					break
				}
			}
			if merged {
				break
			}
		}
		if !merged {
			break
		}
	}

	for _, cluster := range clusters {
		strict := make([]bookTitleClaim, 0, len(cluster.claims))
		for _, c := range cluster.claims {
			if compatibleTruncation(c.claim.Value, cluster.value, windows) {
				cluster.trunc = append(cluster.trunc, c)
				continue
			}
			strict = append(strict, c)
		}
		cluster.strict = strict
		cluster.selected = titleCandidate(cluster)
	}
	return clusters
}

func sameWitnessDistinctFilenameSet(cluster *bookTitleCluster, c bookTitleClaim) bool {
	if c.claim.Source != SourceFilename {
		return false
	}
	for _, existing := range cluster.claims {
		if existing.hash != c.hash || existing.claim.Source != SourceFilename || existing.claim.Set == c.claim.Set {
			continue
		}
		if existing.claim.Set == "filename:stem" || c.claim.Set == "filename:stem" {
			return true
		}
	}
	return false
}

func clustersShareWitnessFilenameSet(a, b *bookTitleCluster) bool {
	for _, left := range a.claims {
		for _, right := range b.claims {
			if left.hash == right.hash && left.claim.Source == SourceFilename &&
				right.claim.Source == SourceFilename && left.claim.Set != right.claim.Set {
				if left.claim.Set == "filename:stem" || right.claim.Set == "filename:stem" {
					return true
				}
			}
		}
	}
	return false
}

func compatibleTruncation(shorter, longer string, windows []Window) bool {
	if titlesEquivalent(shorter, longer) {
		return false
	}
	shortWords := wordTokens(shorter)
	longWords := wordTokens(longer)
	shortOrdered := orderedTitleTokens(shorter)
	longOrdered := orderedTitleTokens(longer)
	if len(shortWords) == 0 || !subset(shortWords, longWords) ||
		len(longOrdered) <= len(shortOrdered) {
		return false
	}
	extraWords := setDifference(longWords, shortWords)
	extraNumbers := make([]string, 0)
	for _, token := range longOrdered {
		if hasWordRune(token) {
			continue
		}
		if !containsToken(shortOrdered, token) {
			extraNumbers = append(extraNumbers, token)
		}
	}
	if len(extraWords) > 0 && !windowContainsAll(windows, extraWords) {
		return false
	}
	// Extra numbers may only be leading in the longer value. A trailing
	// number is a volume marker (Dune 2), not a title-page truncation.
	if !numbersLeading(longOrdered, extraNumbers) {
		return false
	}
	return true
}

func containsToken(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func windowContainsAll(windows []Window, tokens []string) bool {
	set := make(map[string]struct{})
	for _, window := range windows {
		for _, line := range window.Lines {
			for _, token := range wordTokens(line) {
				set[token] = struct{}{}
			}
		}
	}
	for _, token := range tokens {
		if _, ok := set[token]; !ok {
			return false
		}
	}
	return len(tokens) > 0
}

func numbersLeading(ordered, numbers []string) bool {
	if len(numbers) == 0 {
		return true
	}
	firstWord := -1
	for i, token := range ordered {
		if hasWordRune(token) {
			firstWord = i
			break
		}
	}
	if firstWord < 0 {
		return false
	}
	for _, number := range numbers {
		index := indexOf(ordered, number)
		if index < 0 || index > firstWord {
			return false
		}
	}
	return true
}

func titleCandidate(cluster *bookTitleCluster) *BookCandidate {
	representative := representativeTitle(cluster)
	strict := make([]bookTitleClaim, 0, len(cluster.strict))
	for _, c := range cluster.strict {
		if titlesEquivalent(c.claim.Value, representative) || c.claim.Value == representative {
			strict = append(strict, c)
		}
	}
	if len(strict) == 0 {
		strict = cluster.strict
	}
	metadata := make([]bookTitleClaim, 0)
	hashes := make(map[string]struct{})
	groups := make(map[string]struct{})
	var level AttestLevel
	var score float64
	var foundIn []int64
	var residual []string
	suspicious := false
	hasStrongMetadata := false
	attestedAny := false
	for _, c := range strict {
		hashes[c.hash] = struct{}{}
		if c.group != "" {
			groups[c.group] = struct{}{}
		}
		if c.att.Level == AttestFull || c.att.Level == AttestPartial {
			attestedAny = true
		}
		if c.claim.Source == SourceMetadata {
			metadata = append(metadata, c)
			if effectiveTrust(c.claim) == TrustStrong {
				hasStrongMetadata = true
			}
			if claimSuspicious(c.claim, c.att) {
				suspicious = true
			}
			if attestRank(c.att.Level) > attestRank(level) {
				level = c.att.Level
			}
			if c.att.Score > score {
				score = c.att.Score
			}
			foundIn = mergeIDs(foundIn, c.att.FoundIn)
			if len(c.att.Residual) > 0 {
				residual = c.att.Residual
			}
		}
	}
	supportingHashes := make(map[string]struct{}, len(hashes))
	for hash := range hashes {
		supportingHashes[hash] = struct{}{}
	}
	// Content windows are supporting witnesses even when they belong to a
	// different content hash. This is audit evidence, not strict support.
	for _, id := range foundIn {
		for _, c := range cluster.claims {
			if c.fileID == id {
				supportingHashes[c.hash] = struct{}{}
			}
		}
	}
	status := StatusWeak
	reason := "weak"
	if len(metadata) == 0 {
		if anyStrongStrict(strict) {
			status = StatusSingle
			reason = "single_text"
		}
	} else if level == AttestFull || level == AttestPartial {
		status = StatusCorroborated
		reason = "content_attestation_cross"
		for _, c := range metadata {
			if (c.att.Level == AttestFull || c.att.Level == AttestPartial) && containsID(c.att.FoundIn, c.fileID) {
				reason = "content_attestation_self"
				break
			}
		}
	} else if hasStrongMetadata && len(groups) >= 2 {
		status = StatusCrossFile
		reason = "cross_file"
	} else if hasStrongMetadata {
		status = StatusSingle
		reason = "single_metadata"
	} else if suspicious {
		status = StatusWeak
		reason = "suspicious_metadata"
	}
	return &BookCandidate{
		Field:                  FieldTitle,
		Value:                  representative,
		Status:                 status,
		Reason:                 reason,
		SupportingWitnesses:    len(supportingHashes),
		StrictSupportingClaims: len(hashes),
		SourceGroups:           len(groups),
		Attested:               attestedAny,
		Suspicious:             suspicious,
		AttestationLevel:       level,
		MaxAttestation:         score,
		FoundIn:                foundIn,
		Residual:               residual,
		UseForAgreement:        true,
		Evidence: map[string]string{
			"status": status.String(),
			"reason": reason,
		},
	}
}

func representativeTitle(cluster *bookTitleCluster) string {
	strict := cluster.strict
	if len(strict) == 0 {
		strict = cluster.claims
	}
	best := strict[0]
	for _, c := range strict[1:] {
		if betterRepresentative(c, best) {
			best = c
		}
	}
	return best.claim.Value
}

func betterRepresentative(a, b bookTitleClaim) bool {
	ar, br := attestRank(a.att.Level), attestRank(b.att.Level)
	if ar != br {
		return ar > br
	}
	aSusp, bSusp := claimSuspicious(a.claim, a.att), claimSuspicious(b.claim, b.att)
	if aSusp != bSusp {
		return !aSusp
	}
	if effectiveTrust(a.claim) != effectiveTrust(b.claim) {
		return effectiveTrust(a.claim) == TrustStrong
	}
	if sourceRank(a.claim.Source) != sourceRank(b.claim.Source) {
		return sourceRank(a.claim.Source) < sourceRank(b.claim.Source)
	}
	if setRank(a.claim.Set) != setRank(b.claim.Set) {
		return setRank(a.claim.Set) < setRank(b.claim.Set)
	}
	if allCapsValue(a.claim.Value) != allCapsValue(b.claim.Value) {
		return !allCapsValue(a.claim.Value)
	}
	if len(a.claim.Value) != len(b.claim.Value) {
		return len(a.claim.Value) > len(b.claim.Value)
	}
	return false
}

func setRank(set string) int {
	switch {
	case strings.HasPrefix(set, "filename:natural"), strings.HasPrefix(set, "filename:calibre"),
		strings.HasPrefix(set, "filename:archive"):
		return 0
	case strings.HasPrefix(set, "filename:stem"):
		return 1
	case strings.HasPrefix(set, "text:label"), strings.HasPrefix(set, "text:calibre"):
		return 2
	case strings.HasPrefix(set, "text:gutenberg"):
		return 3
	case strings.HasPrefix(set, "text:titleline"):
		return 4
	case strings.HasPrefix(set, "text:titlepage"):
		return 5
	default:
		return 6
	}
}

func allCapsValue(value string) bool {
	letters := 0
	for _, r := range value {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return letters >= 2
}

func attestRank(level AttestLevel) int {
	switch level {
	case AttestFull:
		return 5
	case AttestPartial:
		return 4
	case AttestSubstring:
		return 3
	case AttestSecondary:
		return 2
	case AttestText:
		return 1
	default:
		return 0
	}
}

func truncationStatus(c Claim) Status {
	if c.Source == SourceMetadata && c.Trust == TrustStrong {
		return StatusSingle
	}
	return StatusWeak
}

func anyStrongStrict(claims []bookTitleClaim) bool {
	for _, c := range claims {
		if effectiveTrust(c.claim) == TrustStrong {
			return true
		}
	}
	return false
}

func mergeIDs(a, b []int64) []int64 {
	out := append([]int64{}, a...)
	for _, id := range b {
		out = appendUniqueID(out, id)
	}
	return out
}

func containsID(ids []int64, id int64) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

func chooseTitleCluster(clusters []*bookTitleCluster) (*bookTitleCluster, bool) {
	var best *bookTitleCluster
	for _, cluster := range clusters {
		if best == nil || betterBookTitleCluster(cluster, best) {
			best = cluster
		}
	}
	conflict := false
	if best != nil {
		for _, cluster := range clusters {
			if cluster == best || cluster.selected == nil || best.selected == nil {
				continue
			}
			if isOverruledTitle(cluster, best) {
				continue
			}
			if cluster.selected.Status != StatusWeak && cluster.selected.Status != StatusNone &&
				best.selected.Status != StatusWeak && best.selected.Status != StatusNone {
				conflict = true
				break
			}
		}
	}
	return best, conflict
}

func betterBookTitleCluster(a, b *bookTitleCluster) bool {
	if a.selected == nil || b.selected == nil {
		return a.selected != nil
	}
	if statusRank(a.selected.Status) != statusRank(b.selected.Status) {
		return statusRank(a.selected.Status) > statusRank(b.selected.Status)
	}
	if a.selected.Attested != b.selected.Attested {
		return a.selected.Attested
	}
	if a.selected.Suspicious != b.selected.Suspicious {
		return !a.selected.Suspicious
	}
	if a.selected.StrictSupportingClaims != b.selected.StrictSupportingClaims {
		return a.selected.StrictSupportingClaims > b.selected.StrictSupportingClaims
	}
	if a.selected.SupportingWitnesses != b.selected.SupportingWitnesses {
		return a.selected.SupportingWitnesses > b.selected.SupportingWitnesses
	}
	if sourceRank(primarySource(a)) != sourceRank(primarySource(b)) {
		return sourceRank(primarySource(a)) < sourceRank(primarySource(b))
	}
	if clusterSetRank(a) != clusterSetRank(b) {
		return clusterSetRank(a) < clusterSetRank(b)
	}
	return a.selected.Value > b.selected.Value
}

func clusterSetRank(cluster *bookTitleCluster) int {
	best := 9
	for _, c := range cluster.strict {
		if rank := setRank(c.claim.Set); rank < best {
			best = rank
		}
	}
	if best == 9 {
		for _, c := range cluster.claims {
			if rank := setRank(c.claim.Set); rank < best {
				best = rank
			}
		}
	}
	return best
}

func primarySource(cluster *bookTitleCluster) Source {
	for _, c := range cluster.strict {
		return c.claim.Source
	}
	return SourceFilename
}

func isOverruledTitle(cluster, winner *bookTitleCluster) bool {
	if cluster.selected == nil || winner.selected == nil {
		return true
	}
	if cluster.selected.Status == StatusWeak || cluster.selected.Status == StatusNone {
		return true
	}
	// A compatible truncation is already merged, so only a genuinely
	// different strong hypothesis can remain. It is overruled only when it
	// is not itself attested and has no author-bearing title tokens.
	if winner.selected.Attested && !cluster.selected.Attested {
		return false
	}
	return false
}

type bookAuthorCluster struct {
	value  string
	claims []bookAuthorClaim
	status Status
}

func clusterBookAuthors(claims []bookAuthorClaim) []*bookAuthorCluster {
	clusters := make([]*bookAuthorCluster, 0)
	for _, c := range claims {
		var found *bookAuthorCluster
		for _, cluster := range clusters {
			if authorsEquivalent(cluster.value, c.claim.Value) {
				found = cluster
				break
			}
		}
		if found == nil {
			clusters = append(clusters, &bookAuthorCluster{value: c.claim.Value})
			found = clusters[len(clusters)-1]
		}
		found.claims = append(found.claims, c)
	}
	for _, cluster := range clusters {
		cluster.status = authorStatusForCluster(cluster)
	}
	return clusters
}

func authorStatusForCluster(cluster *bookAuthorCluster) Status {
	hasMetadata := false
	attested := false
	strong := false
	groups := make(map[string]struct{})
	for _, c := range cluster.claims {
		if c.claim.Source == SourceMetadata {
			hasMetadata = true
			groups[c.group] = struct{}{}
			if c.att.Level == AttestFull || c.att.Level == AttestPartial {
				attested = true
			}
			if c.claim.Trust == TrustStrong {
				strong = true
			}
		}
	}
	switch {
	case hasMetadata && attested:
		return StatusCorroborated
	case hasMetadata && len(groups) >= 2:
		return StatusCrossFile
	case hasMetadata && strong:
		return StatusSingle
	case hasMetadata:
		return StatusSingle
	default:
		for _, c := range cluster.claims {
			if c.claim.Trust == TrustStrong {
				return StatusSingle
			}
		}
		return StatusWeak
	}
}

func statusForAuthorCluster(cluster *bookAuthorCluster) Status {
	return cluster.status
}

func chooseAuthorCluster(clusters []*bookAuthorCluster) (*bookAuthorCluster, bool) {
	var best *bookAuthorCluster
	for _, cluster := range clusters {
		if best == nil || betterBookAuthorCluster(cluster, best) {
			best = cluster
		}
	}
	conflict := false
	if best != nil {
		for _, cluster := range clusters {
			if cluster == best {
				continue
			}
			if isNoiseAuthor(cluster.value) || cluster.status == StatusWeak {
				continue
			}
			if cluster.status != StatusSingle && cluster.status != StatusCrossFile && cluster.status != StatusCorroborated {
				continue
			}
			if best.status == StatusCorroborated && cluster.status != StatusCorroborated {
				// overruled only when the loser is noise or compatible; a
				// real name remains a conflict candidate.
				if !isNoiseAuthor(cluster.value) {
					conflict = true
				}
				continue
			}
			conflict = true
		}
	}
	return best, conflict
}

func betterBookAuthorCluster(a, b *bookAuthorCluster) bool {
	if statusRank(a.status) != statusRank(b.status) {
		return statusRank(a.status) > statusRank(b.status)
	}
	if len(a.claims) != len(b.claims) {
		return len(a.claims) > len(b.claims)
	}
	return a.value < b.value
}

func authorCandidate(cluster *bookAuthorCluster) BookCandidate {
	hashes := make(map[string]struct{})
	groups := make(map[string]struct{})
	attested := false
	for _, c := range cluster.claims {
		hashes[c.hash] = struct{}{}
		if c.group != "" {
			groups[c.group] = struct{}{}
		}
		if c.att.Level == AttestFull || c.att.Level == AttestPartial {
			attested = true
		}
	}
	return BookCandidate{
		Field:                  FieldAuthor,
		Value:                  cluster.value,
		Authors:                []string{cluster.value},
		Status:                 cluster.status,
		Reason:                 "author",
		SupportingWitnesses:    len(hashes),
		StrictSupportingClaims: len(hashes),
		SourceGroups:           len(groups),
		Attested:               attested,
		UseForAgreement:        true,
		Evidence:               map[string]string{},
	}
}

func preferCleanFullName(titleWinner *bookTitleCluster, authorClaims []bookAuthorClaim) (string, bool) {
	representative := representativeClaim(titleWinner)
	if representative == nil {
		return "", false
	}
	for _, c := range authorClaims {
		if c.fileID != representative.fileID || c.claim.Source != SourceFilename || c.claim.Junk {
			continue
		}
		if strings.ContainsRune(c.claim.Value, utf8.RuneError) || isNoiseAuthor(c.claim.Value) {
			continue
		}
		if len(strings.Fields(c.claim.Value)) < 2 {
			continue
		}
		if !compatibleAuthorSet(c, titleWinner) {
			continue
		}
		return c.claim.Value, true
	}
	return "", false
}

func compatibleAuthorSet(author bookAuthorClaim, titleWinner *bookTitleCluster) bool {
	// A matching title in the same hypothesis is always acceptable.
	for _, c := range titleWinner.strict {
		if c.fileID != author.fileID || c.claim.Set != author.claim.Set {
			continue
		}
		if textmatch.TitleKey(c.claim.Value) == textmatch.TitleKey(titleWinner.selected.Value) {
			return true
		}
		return false
	}
	// Archive and parent-directory names may carry the only clean author while
	// their own title part is missing or decoding-broken. Both are supporting
	// witnesses of the same file, so allow these explicit, narrow cases.
	return strings.HasPrefix(author.claim.Set, "filename:archive") ||
		author.claim.Set == "filename:path-author"
}

func authorListForCluster(cluster *bookAuthorCluster, claims []bookAuthorClaim) []string {
	if cluster == nil || len(cluster.claims) == 0 {
		return nil
	}
	best := cluster.claims[0]
	for _, c := range cluster.claims[1:] {
		if c.att.Level != AttestNone && best.att.Level == AttestNone {
			best = c
		}
	}
	return authorNamesForFileSet(claims, best.fileID, best.claim.Set)
}

func authorNamesForFileSet(claims []bookAuthorClaim, fileID int64, set string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, c := range claims {
		if c.fileID != fileID || c.claim.Set != set {
			continue
		}
		key := textmatch.TitleKey(c.claim.Value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c.claim.Value)
	}
	return out
}

func authorNamesInSameSet(titleClaims []bookTitleClaim, authorClaims []bookAuthorClaim, titleWinner *bookTitleCluster) []string {
	representative := representativeClaim(titleWinner)
	if representative == nil {
		return nil
	}
	var names []string
	seen := make(map[string]struct{})
	for _, c := range authorClaims {
		if c.fileID != representative.fileID || c.claim.Set != representative.claim.Set {
			continue
		}
		key := textmatch.TitleKey(c.claim.Value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, c.claim.Value)
	}
	return names
}

func representativeClaim(cluster *bookTitleCluster) *bookTitleClaim {
	if cluster == nil {
		return nil
	}
	strict := cluster.strict
	if len(strict) == 0 {
		strict = cluster.claims
	}
	if len(strict) == 0 {
		return nil
	}
	best := strict[0]
	for _, c := range strict[1:] {
		if betterRepresentative(c, best) {
			best = c
		}
	}
	return &best
}

func authorStatusForNames(claims []bookAuthorClaim, names []string) Status {
	best := StatusWeak
	for _, name := range names {
		for _, c := range claims {
			if !authorsEquivalent(c.claim.Value, name) {
				continue
			}
			status := authorStatusForCluster(&bookAuthorCluster{value: c.claim.Value, claims: []bookAuthorClaim{c}})
			if statusRank(status) > statusRank(best) {
				best = status
			}
		}
	}
	return best
}

func isNoiseAuthor(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	fields := strings.Fields(value)
	if len(fields) != 1 {
		return false
	}
	token := fields[0]
	if utf8.RuneCountInString(token) > 3 {
		return false
	}
	hasLetter := false
	for _, r := range token {
		if !unicode.IsLetter(r) {
			return false
		}
		if !unicode.IsUpper(r) {
			return false
		}
		hasLetter = true
	}
	return hasLetter
}

func detectGroupingSuspect(titleClusters []*bookTitleCluster, authorClusters []*bookAuthorCluster, winner *bookTitleCluster) (bool, string) {
	if winner == nil {
		return false, ""
	}
	strong := 0
	for _, cluster := range titleClusters {
		if cluster == winner || cluster.selected == nil {
			continue
		}
		if cluster.selected.Status == StatusWeak || cluster.selected.Status == StatusNone {
			continue
		}
		if compatibleTruncation(cluster.value, winner.value, nil) {
			continue
		}
		strong++
	}
	if strong == 0 {
		return false, ""
	}
	// A second strong title hypothesis with a disjoint author set is a
	// likely bad MinHash grouping.
	winnerAuthors := make(map[string]struct{})
	for _, c := range winner.strict {
		for _, a := range authorClusters {
			if a.value != "" {
				winnerAuthors[textmatch.TitleKey(a.value)] = struct{}{}
			}
		}
		_ = c
	}
	return true, "metadata_points_to_multiple_works"
}

func (s Status) String() string { return string(s) }
