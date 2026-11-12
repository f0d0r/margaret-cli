// Package claim collects title and author statements from metadata, text and
// file names, marks the ones the leading text supports, and resolves them
// into a per-file title and author list with provenance.
package claim

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/f0d0r/margaret-cli/internal/filemeta"
	"github.com/f0d0r/margaret-cli/internal/metaheur"
	"github.com/f0d0r/margaret-cli/internal/textmatch"
)

// Versions identify the extraction/attestation and resolution rules. Bump
// ClaimVersion when the stored claims are no longer valid; bump ResolveVersion
// when only the resolution rules changed and the stored claims can be reused.
const (
	// ClaimVersion 6 restricts text ISBNs to standalone lines and extracts
	// subtitle/multi-author title pages.
	ClaimVersion   = 6
	ResolveVersion = 3
)

// Field is the metadata field a claim describes.
type Field string

const (
	FieldTitle  Field = "title"
	FieldAuthor Field = "author"
)

// Source records where a claim came from.
type Source string

const (
	SourceMetadata Source = "METADATA"
	SourceText     Source = "TEXT"
	SourceFilename Source = "FILENAME"
)

// Trust is the strength of a claim as a hypothesis.
type Trust string

const (
	TrustStrong Trust = "STRONG"
	TrustWeak   Trust = "WEAK"
)

// Witness describes how independent a claim is from the other sources.
type Witness string

const (
	WitnessIndependent Witness = "INDEPENDENT"
	WitnessSupporting  Witness = "SUPPORTING"
	WitnessDerived     Witness = "DERIVED"
)

// Status is the resolved strength of a title or author field.
type Status string

const (
	StatusNone         Status = "none"
	StatusWeak         Status = "weak"
	StatusSingle       Status = "single"
	StatusCrossFile    Status = "cross_file"
	StatusCorroborated Status = "corroborated"
	StatusConflict     Status = "conflict"
)

// Set is one extraction hypothesis: a title and its authors as produced by a
// single source (metadata block, text heuristic or file-name parse).
type Set struct {
	Key     string
	Source  Source
	Origin  string
	Path    string
	Title   string
	Authors []string
	Trust   Trust
	Witness Witness
}

// Claim is one title or author statement with provenance and text support.
type Claim struct {
	Field               Field
	Value               string
	Source              Source
	Set                 string
	Origin              string
	Path                string
	Ordinal             int
	FileID              int64
	Junk                bool
	Trust               Trust
	Witness             Witness
	Attested            bool
	Attestation         float64
	AttestationMethod   string
	AttestationLevel    AttestLevel
	AttestationResidual []string
	SuspiciousSignals   int64
}

const (
	SuspiciousLong int64 = 1 << iota
	SuspiciousCommas
	SuspiciousQuote
	SuspiciousSentence
)

// TitleSignals stores the raw, parse-time signals used later to mark a title
// claim as suspicious. Attestation decides whether the signals actually lower
// the claim's trust.
func TitleSignals(value string) int64 {
	value = strings.TrimSpace(value)
	var signals int64
	if runeLen(value) > 100 {
		signals |= SuspiciousLong
	}
	if strings.Count(value, ",") >= 2 {
		signals |= SuspiciousCommas
	}
	if unbalancedQuote(value) {
		signals |= SuspiciousQuote
	}
	words := strings.Fields(value)
	if len(words) >= 12 && (strings.Contains(value, "(") || strings.Count(value, ",") >= 1) {
		signals |= SuspiciousSentence
	}
	return signals
}

func unbalancedQuote(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	open := 0
	close := 0
	for _, r := range value {
		switch r {
		case '"', '„', '“':
			open++
		case '”':
			close++
		}
	}
	if open == 0 {
		return false
	}
	return open != close
}

func signalCount(signals int64) int {
	count := 0
	for signals != 0 {
		signals &= signals - 1
		count++
	}
	return count
}

func effectiveTrust(c Claim) Trust {
	if c.Field == FieldTitle && !c.Attested && signalCount(c.SuspiciousSignals) >= 2 {
		return TrustWeak
	}
	return c.Trust
}

func claimSuspicious(c Claim, att Attestation) bool {
	return c.Field == FieldTitle && att.Level == AttestNone &&
		IsSuspicious(c.SuspiciousSignals, c.Attested)
}

// IsSuspicious reports whether the raw title signals lower a claim's trust.
func IsSuspicious(signals int64, attested bool) bool {
	return !attested && signalCount(signals) >= 2
}

// Resolution is the per-file decision built from the claims.
type Resolution struct {
	Title        string
	Authors      []string
	TitleStatus  Status
	AuthorStatus Status
	TitleSet     string
	AuthorSet    string
	TitleReason  string
	AuthorReason string
}

// Build expands sets into claims, marks junk values and attests metadata and
// file-name claims against the leading text.
func Build(sets []Set, head, path string) []Claim {
	claims := make([]Claim, 0, len(sets)*2)
	seen := make(map[string]struct{})
	add := func(c Claim) {
		key := string(c.Source) + "\x1f" + c.Path + "\x1f" + c.Set + "\x1f" +
			string(c.Field) + "\x1f" + c.Value
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if c.Field == FieldTitle {
			c.SuspiciousSignals = TitleSignals(c.Value)
		}
		claims = append(claims, c)
	}
	for _, set := range sets {
		if set.Path == "" && set.Source == SourceFilename {
			set.Path = path
		}
		if strings.TrimSpace(set.Title) != "" {
			add(Claim{
				Field:   FieldTitle,
				Value:   strings.TrimSpace(set.Title),
				Source:  set.Source,
				Set:     set.Key,
				Origin:  set.Origin,
				Path:    set.Path,
				Trust:   set.Trust,
				Witness: set.Witness,
			})
		}
		for i, author := range set.Authors {
			author = strings.TrimSpace(author)
			if author == "" {
				continue
			}
			add(Claim{
				Field:   FieldAuthor,
				Value:   author,
				Source:  set.Source,
				Set:     set.Key,
				Origin:  set.Origin,
				Path:    set.Path,
				Ordinal: i,
				Trust:   set.Trust,
				Witness: set.Witness,
			})
		}
	}

	titles := make([]string, 0)
	for i := range claims {
		claims[i].Junk = isJunk(claims[i])
		if claims[i].Field == FieldTitle && !claims[i].Junk {
			titles = append(titles, claims[i].Value)
		}
	}
	for i := range claims {
		if claims[i].Field == FieldAuthor && !claims[i].Junk {
			for _, title := range titles {
				if sameFold(claims[i].Value, title) {
					claims[i].Junk = true
					break
				}
			}
		}
	}
	for i := range claims {
		if claims[i].Junk {
			claims[i].AttestationMethod = "none"
			continue
		}
		if claims[i].Source == SourceText {
			claims[i].Attested = true
			claims[i].Attestation = 1
			claims[i].AttestationMethod = "text"
			claims[i].AttestationLevel = AttestText
			continue
		}
		att := AttestInHead(head, claims[i].Field, claims[i].Value)
		claims[i].Attested = att.Level == AttestFull || att.Level == AttestPartial
		claims[i].Attestation = att.Score
		claims[i].AttestationMethod = att.Method
		claims[i].AttestationLevel = att.Level
		claims[i].AttestationResidual = att.Residual
	}
	return claims
}

// WindowLines returns the normalized title-page lines stored per file for
// later book-level attestation.
func WindowLines(head string) []string {
	primary, _ := splitHead(head, 8<<10)
	return titlePageLines(primary)
}

// Attest reports whether value appears as a title-page-like phrase in the
// first 8 KiB of head. The secondary 8-64 KiB range is recorded for audit but
// never sets attested.
func Attest(head string, field Field, value string) (bool, float64, string) {
	att := AttestInHead(head, field, value)
	attested := att.Level == AttestFull || att.Level == AttestPartial
	return attested, att.Score, att.Method
}

// AttestInHead checks one claim against the file's own title window. The
// returned level is the shared policy used by file- and book-level
// resolution.
func AttestInHead(head string, field Field, value string) Attestation {
	value = strings.TrimSpace(value)
	if value == "" {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	primary, secondary := splitHead(head, 8<<10)
	att := AttestValue(field, value, []Window{{Lines: titlePageLines(primary)}})
	if att.Level != AttestNone {
		return att
	}
	var tokens []string
	if field == FieldAuthor {
		tokens = textmatch.AuthorTokens(value)
	} else {
		tokens = orderedTitleTokens(value)
	}
	if len(tokens) < 2 {
		return Attestation{Level: AttestNone, Method: "none"}
	}
	secondaryTokens := orderedTitleTokens(secondary)
	if field == FieldAuthor {
		secondaryTokens = textmatch.AuthorTokens(secondary)
	}
	if allTokensPresent(secondaryTokens, tokens) {
		return Attestation{
			Level:  AttestSecondary,
			Score:  tokenCoverage(tokens, secondaryTokens),
			Method: "secondary_only",
		}
	}
	return Attestation{Level: AttestNone, Method: "none"}
}

// orderedTitleTokens keeps the word order so a title can be matched as a
// phrase. Punctuation is folded by TitleKey; stopwords stay part of the
// phrase.
func orderedTitleTokens(s string) []string {
	return strings.Fields(textmatch.TitleKey(s))
}

// sequenceMatch reports whether all needles appear in order in haystack with
// at most maxGap extra tokens between consecutive needles. exact means the
// match is contiguous.
func sequenceMatch(haystack, needles []string, maxGap int) (matched, exact bool) {
	if len(needles) == 0 {
		return false, false
	}
	for start := 0; start < len(haystack); start++ {
		if haystack[start] != needles[0] {
			continue
		}
		pos := start
		ok := true
		gaps := 0
		for _, needle := range needles[1:] {
			found := -1
			for j := pos + 1; j < len(haystack) && j <= pos+1+maxGap; j++ {
				if haystack[j] == needle {
					found = j
					break
				}
			}
			if found < 0 {
				ok = false
				break
			}
			if found != pos+1 {
				gaps++
			}
			pos = found
		}
		if ok {
			return true, gaps == 0
		}
	}
	return false, false
}

func allTokensPresent(haystack, needles []string) bool {
	set := make(map[string]struct{}, len(haystack))
	for _, token := range haystack {
		set[token] = struct{}{}
	}
	for _, needle := range needles {
		if _, ok := set[needle]; !ok {
			return false
		}
	}
	return true
}

func splitHead(head string, primaryBytes int) (string, string) {
	if len(head) <= primaryBytes {
		return head, ""
	}
	cut := primaryBytes
	for cut > 0 && !utf8.RuneStart(head[cut]) {
		cut--
	}
	return head[:cut], head[cut:]
}

var (
	pageNumber = regexp.MustCompile(`^[0-9]{1,4}$|^[ivxlcdm]{1,7}$`)
	lineNoise  = regexp.MustCompile(`(?i)^(isbn|issn)[\s:.-]`)
)

// titlePageLines keeps short, non-prose lines from the leading text. They are
// the only places where a metadata or file-name claim can be attested.
func titlePageLines(head string) []string {
	head = strings.TrimPrefix(head, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(head, "\r\n", "\n"), "\r", "\n"), "\n")
	out := make([]string, 0, 12)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if len(out) >= 24 {
			break
		}
		folded := strings.ToLower(line)
		if pageNumber.MatchString(folded) || lineNoise.MatchString(folded) {
			continue
		}
		if metaheur.IsJunkValue(line) || metaheur.IsCredit(line) ||
			metaheur.IsChapterHeading(line) || metaheur.LooksLikeProse(line) {
			continue
		}
		if runeLen(line) > 200 || strings.HasSuffix(line, ".") && len(strings.Fields(line)) >= 5 {
			continue
		}
		if len(out) > 0 && isBodyLine(line) {
			break
		}
		out = append(out, line)
	}
	return out
}

func isBodyLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "–") || strings.HasPrefix(line, "—") || strings.HasPrefix(line, "- ") {
		return true
	}
	words := strings.Fields(line)
	if len(words) < 5 {
		return false
	}
	last := line[len(line)-1]
	return last == '.' || last == '!' || last == '?'
}

func isJunk(c Claim) bool {
	value := strings.TrimSpace(c.Value)
	if value == "" {
		return true
	}
	switch c.Field {
	case FieldTitle:
		return filemeta.IsPlaceholderTitle(value) || metaheur.IsJunkValue(value) ||
			metaheur.IsCredit(value) || metaheur.IsArtifactTitle(value) ||
			metaheur.IsChapterHeading(value)
	case FieldAuthor:
		return isPlaceholderAuthor(value) || metaheur.IsJunkValue(value) ||
			metaheur.IsCredit(value) || !hasLetter(value)
	default:
		return true
	}
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func sameFold(a, b string) bool {
	return textmatch.TitleKey(a) == textmatch.TitleKey(b)
}

var placeholderAuthors = map[string]struct{}{
	"ismeretlen":         {},
	"szerzo ismeretlen":  {},
	"nevtelen":           {},
	"unknown":            {},
	"unknown author":     {},
	"author unknown":     {},
	"no author":          {},
	"untitled":           {},
	"anonymous":          {},
	"none":               {},
	"n/a":                {},
	"n.n.":               {},
	"inconnu":            {},
	"inconnue":           {},
	"auteur inconnu":     {},
	"anonyme":            {},
	"unbekannt":          {},
	"anonym":             {},
	"desconocido":        {},
	"desconocida":        {},
	"desconhecido":       {},
	"desconhecida":       {},
	"sconosciuto":        {},
	"sconosciuta":        {},
	"autore sconosciuto": {},
	"anonimo":            {},
	"onbekend":           {},
	"onbekende auteur":   {},
	"necunoscut":         {},
	"autor necunoscut":   {},
	"anonim":             {},
	"neznamy":            {},
	"neznama":            {},
	"neznamy autor":      {},
	"nieznany":           {},
	"nieznana":           {},
	"autor nieznany":     {},
	"okand":              {},
	"okant":              {},
	"okand forfattare":   {},
	"bilinmiyor":         {},
	"bilinmeyen yazar":   {},
	"user":               {},
	"-":                  {},
	"?":                  {},
	"":                   {},
}

func isPlaceholderAuthor(s string) bool {
	_, ok := placeholderAuthors[textmatch.TitleKey(s)]
	return ok
}

// Resolve selects the best title and author list from claims.
func Resolve(claims []Claim, path string) Resolution {
	claims = selectFilenamePath(claims, path)
	titleClaims := filterField(claims, FieldTitle)
	authorClaims := filterField(claims, FieldAuthor)

	titleClusters := clusterTitles(titleClaims)
	authorClusters := clusterAuthors(authorClaims)

	titleBest, titleConflict := bestTitleCluster(titleClusters)
	authorBest, authorConflict := bestAuthorCluster(authorClusters)

	res := Resolution{TitleStatus: StatusNone, AuthorStatus: StatusNone}
	if titleBest != nil {
		// A filename-only title is a weak fallback; leave the stored title
		// empty so the book-level filename fallback keeps its old behaviour.
		if titleBest.status != StatusWeak || !onlySource(titleBest.claims, SourceFilename) {
			res.Title = titleBest.value
		}
		res.TitleStatus = titleBest.status
		res.TitleSet = titleBest.set
		if titleConflict {
			res.TitleStatus = StatusConflict
			res.TitleReason = "conflicting_title_hypotheses"
		}
	}
	if authorBest != nil {
		if authorBest.status != StatusWeak || !onlySource(authorBest.claims, SourceFilename) {
			res.Authors = authorBest.names
		}
		res.AuthorStatus = authorBest.status
		res.AuthorSet = authorBest.set
		if authorConflict {
			res.AuthorStatus = StatusConflict
			res.AuthorReason = "conflicting_author_hypotheses"
		}
	}

	// Prefer the title hypothesis' own author list when it is at least as
	// strong as the independent author winner.
	if titleBest != nil && titleBest.set != "" {
		names := authorNamesForSet(authorClaims, titleBest.set)
		if len(names) > 0 {
			status := authorListStatus(names, authorClusters)
			filenameOnly := status == StatusWeak && setOnlySource(authorClaims, titleBest.set, SourceFilename)
			if !filenameOnly && (authorBest == nil || statusRank(status) >= statusRank(res.AuthorStatus)) {
				res.Authors = names
				res.AuthorStatus = status
				res.AuthorSet = titleBest.set
				if authorConflict {
					res.AuthorStatus = StatusConflict
					res.AuthorReason = "conflicting_author_hypotheses"
				}
			}
		}
	}
	return res
}

func selectFilenamePath(claims []Claim, path string) []Claim {
	hasPath := false
	for _, c := range claims {
		if c.Source == SourceFilename && c.Path == path {
			hasPath = true
			break
		}
	}
	if !hasPath {
		return claims
	}
	out := claims[:0]
	for _, c := range claims {
		if c.Source != SourceFilename || c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func onlySource(claims []Claim, source Source) bool {
	if len(claims) == 0 {
		return false
	}
	for _, c := range claims {
		if c.Source != source {
			return false
		}
	}
	return true
}

func setOnlySource(claims []Claim, set string, source Source) bool {
	found := false
	for _, c := range claims {
		if c.Set != set {
			continue
		}
		found = true
		if c.Source != source {
			return false
		}
	}
	return found
}

func filterField(claims []Claim, field Field) []Claim {
	out := make([]Claim, 0, len(claims))
	for _, c := range claims {
		if c.Field == field && !c.Junk {
			out = append(out, c)
		}
	}
	return out
}

type titleCluster struct {
	value   string
	claims  []Claim
	status  Status
	set     string
	rank    int
	first   int
	source  Source
	strong  bool
	derived bool
}

func clusterTitles(claims []Claim) []*titleCluster {
	var clusters []*titleCluster
	for i, c := range claims {
		var found *titleCluster
		for _, cluster := range clusters {
			if titlesEquivalent(cluster.value, c.Value) {
				found = cluster
				break
			}
		}
		if found == nil {
			clusters = append(clusters, &titleCluster{value: c.Value, first: i, source: c.Source, set: c.Set})
			found = clusters[len(clusters)-1]
		}
		found.claims = append(found.claims, c)
	}
	for _, cluster := range clusters {
		cluster.status = clusterStatus(cluster.claims)
		cluster.rank = statusRank(cluster.status)
		cluster.strong, cluster.derived = strongNonDerived(cluster.claims)
	}
	return clusters
}

func bestTitleCluster(clusters []*titleCluster) (*titleCluster, bool) {
	var best *titleCluster
	for _, cluster := range clusters {
		if best == nil || betterTitleCluster(cluster, best) {
			best = cluster
		}
	}
	conflict := false
	if best != nil {
		for _, cluster := range clusters {
			if cluster == best || !cluster.strong || !best.strong {
				continue
			}
			if !titlesEquivalent(cluster.value, best.value) {
				conflict = true
				break
			}
		}
	}
	return best, conflict
}

func betterTitleCluster(a, b *titleCluster) bool {
	if a.rank != b.rank {
		return a.rank > b.rank
	}
	if clusterAttested(a) != clusterAttested(b) {
		return clusterAttested(a)
	}
	if clusterSuspicious(a) != clusterSuspicious(b) {
		return !clusterSuspicious(a)
	}
	if sourceRank(a.source) != sourceRank(b.source) {
		return sourceRank(a.source) < sourceRank(b.source)
	}
	return a.first < b.first
}

func clusterAttested(cluster *titleCluster) bool {
	for _, c := range cluster.claims {
		if c.Attested {
			return true
		}
	}
	return false
}

func clusterSuspicious(cluster *titleCluster) bool {
	for _, c := range cluster.claims {
		if IsSuspicious(c.SuspiciousSignals, c.Attested) {
			return true
		}
	}
	return false
}

type authorCluster struct {
	value  string
	names  []string
	claims []Claim
	status Status
	set    string
	rank   int
	first  int
	source Source
	strong bool
}

func clusterAuthors(claims []Claim) []*authorCluster {
	var clusters []*authorCluster
	for i, c := range claims {
		var found *authorCluster
		for _, cluster := range clusters {
			if authorsEquivalent(cluster.value, c.Value) {
				found = cluster
				break
			}
		}
		if found == nil {
			clusters = append(clusters, &authorCluster{value: c.Value, first: i, source: c.Source, set: c.Set})
			found = clusters[len(clusters)-1]
		}
		found.claims = append(found.claims, c)
	}
	for _, cluster := range clusters {
		cluster.status = clusterStatus(cluster.claims)
		cluster.rank = statusRank(cluster.status)
		cluster.strong, _ = strongNonDerived(cluster.claims)
		cluster.names = authorNamesForSet(claims, cluster.set)
	}
	return clusters
}

func bestAuthorCluster(clusters []*authorCluster) (*authorCluster, bool) {
	var best *authorCluster
	for _, cluster := range clusters {
		if best == nil || betterAuthorCluster(cluster, best) {
			best = cluster
		}
	}
	conflict := false
	if best != nil {
		bestNames := authorListNames(clusters, best.set)
		for _, cluster := range clusters {
			if cluster.set == best.set || !cluster.strong || !best.strong {
				continue
			}
			if !authorInList(cluster.value, bestNames) {
				conflict = true
				break
			}
		}
	}
	return best, conflict
}

func authorListNames(clusters []*authorCluster, set string) []string {
	var out []string
	for _, cluster := range clusters {
		if cluster.set == set {
			out = append(out, cluster.value)
		}
	}
	return out
}

func authorInList(name string, names []string) bool {
	for _, candidate := range names {
		if authorsEquivalent(name, candidate) {
			return true
		}
	}
	return false
}

func betterAuthorCluster(a, b *authorCluster) bool {
	if a.rank != b.rank {
		return a.rank > b.rank
	}
	if sourceRank(a.source) != sourceRank(b.source) {
		return sourceRank(a.source) < sourceRank(b.source)
	}
	return a.first < b.first
}

func authorListStatus(names []string, clusters []*authorCluster) Status {
	status := StatusCorroborated
	for _, name := range names {
		found := false
		for _, cluster := range clusters {
			if !authorsEquivalent(cluster.value, name) {
				continue
			}
			found = true
			if statusRank(cluster.status) < statusRank(status) {
				status = cluster.status
			}
			break
		}
		if !found {
			return StatusWeak
		}
	}
	return status
}

func authorNamesForSet(claims []Claim, set string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, c := range claims {
		if c.Set != set {
			continue
		}
		key := textmatch.TitleKey(c.Value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c.Value)
	}
	return out
}

func clusterStatus(claims []Claim) Status {
	if len(claims) == 0 {
		return StatusNone
	}
	var (
		hasMetadata        bool
		hasStrongMetadata  bool
		hasTextStrongIndep bool
		hasTextStrongDeriv bool
		hasTextWeak        bool
		hasFilename        bool
		metadataAtt        bool
	)
	for _, c := range claims {
		switch c.Source {
		case SourceMetadata:
			hasMetadata = true
			if effectiveTrust(c) == TrustStrong {
				hasStrongMetadata = true
			}
			if c.Attested {
				metadataAtt = true
			}
		case SourceText:
			if effectiveTrust(c) == TrustStrong {
				if c.Witness == WitnessDerived {
					hasTextStrongDeriv = true
				} else {
					hasTextStrongIndep = true
				}
			} else {
				hasTextWeak = true
			}
		case SourceFilename:
			hasFilename = true
		}
	}
	switch {
	case hasMetadata && metadataAtt && hasTextStrongDeriv && !hasTextStrongIndep:
		// The only matching text is likely a converter-generated title page.
		return StatusSingle
	case hasMetadata && metadataAtt:
		return StatusCorroborated
	case hasStrongMetadata && hasTextStrongIndep:
		return StatusCorroborated
	case hasStrongMetadata:
		return StatusSingle
	case hasMetadata:
		return StatusWeak
	case hasTextStrongIndep && hasFilename:
		return StatusCorroborated
	case hasTextStrongIndep:
		return StatusSingle
	case hasTextWeak || hasFilename:
		return StatusWeak
	default:
		return StatusNone
	}
}

func strongNonDerived(claims []Claim) (strong, derived bool) {
	for _, c := range claims {
		if effectiveTrust(c) == TrustStrong && c.Witness != WitnessDerived {
			strong = true
		}
		if effectiveTrust(c) == TrustStrong && c.Witness == WitnessDerived {
			derived = true
		}
	}
	return strong, derived
}

func statusRank(status Status) int {
	switch status {
	case StatusCorroborated:
		return 5
	case StatusCrossFile:
		return 4
	case StatusSingle:
		return 3
	case StatusWeak:
		return 2
	case StatusConflict:
		return 1
	default:
		return 0
	}
}

func sourceRank(source Source) int {
	switch source {
	case SourceMetadata:
		return 0
	case SourceText:
		return 1
	default:
		return 2
	}
}

func titlesEquivalent(a, b string) bool {
	ka, kb := textmatch.TitleKey(a), textmatch.TitleKey(b)
	if ka == "" || kb == "" {
		return false
	}
	if ka == kb {
		return true
	}
	ta, tb := textmatch.ContentTokens(a), textmatch.ContentTokens(b)
	if len(ta) < 2 || len(tb) < 2 {
		return false
	}
	if !sameNumbers(ta, tb) {
		return false
	}
	return subset(ta, tb) || subset(tb, ta)
}

func authorsEquivalent(a, b string) bool {
	ta, tb := textmatch.AuthorTokens(a), textmatch.AuthorTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return sameFold(a, b)
	}
	return subset(ta, tb) || subset(tb, ta)
}

func subset(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, token := range b {
		set[token] = struct{}{}
	}
	for _, token := range a {
		if _, ok := set[token]; !ok {
			return false
		}
	}
	return true
}

func sameNumbers(a, b []string) bool {
	na, nb := numbers(a), numbers(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

func numbers(tokens []string) []string {
	var out []string
	for _, token := range tokens {
		if strings.IndexFunc(token, func(r rune) bool { return unicode.IsDigit(r) }) >= 0 {
			out = append(out, token)
		}
	}
	return out
}
