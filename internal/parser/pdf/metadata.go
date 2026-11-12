package pdf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/f0d0r/margaret-cli/internal/claim"
	"github.com/f0d0r/margaret-cli/internal/identifier"
	"github.com/f0d0r/margaret-cli/internal/metaheur"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/textdoc"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
	"golang.org/x/net/html/charset"
)

// metadata is the merged PDF metadata before it is overlaid on the
// text-derived values.
type metadata struct {
	title           string
	authors         []string
	description     string
	subjects        []string
	publisher       string
	publicationDate string
	series          string
	seriesIndex     string
	languages       []string
	identifiers     []identifier.Identifier
	modDate         time.Time
	xmpDate         time.Time
}

// infoFields holds the key/value pairs printed by pdfinfo.
type infoFields map[string]string

// parseInfo parses the line-oriented pdfinfo output. Values may contain a
// colon; only the first one separates the key.
func parseInfo(raw string) infoFields {
	fields := make(infoFields)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		fields[key] = decodePDFText(strings.TrimSpace(value))
	}
	return fields
}

func (f infoFields) get(key string) string {
	return strings.TrimSpace(f[key])
}

func (f infoFields) values() []string {
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(f[key]); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// metadataFromInfo maps the PDF Info dictionary to our metadata fields.
// Subject is treated as a description, matching the RTF parser; Keywords are
// treated as subjects.
func metadataFromInfo(info infoFields) metadata {
	return metadata{
		title:       cleanTitle(info.get("Title")),
		authors:     cleanAuthors([]string{info.get("Author")}),
		description: cleanOptional(info.get("Subject")),
		subjects:    splitSubjects(info.get("Keywords")),
		modDate:     parsePDFDate(info.get("ModDate")),
		identifiers: identifiersFromTexts(info.values()),
	}
}

// readMetadata reads the Info dictionary and, when present, the XMP packet.
// XMP errors are ignored: the Info dictionary remains a usable fallback.
func readMetadata(ctx context.Context, data book.Blob, size int64, tools Tools) (metadata, error) {
	rawInfo, err := runInfo(ctx, data, size, tools.Info, "-enc", "UTF-8", "-isodates", "-")
	if err != nil {
		return metadata{}, err
	}
	info := parseInfo(rawInfo)
	if strings.EqualFold(info.get("Encrypted"), "yes") {
		return metadata{}, ErrEncrypted
	}

	md := metadataFromInfo(info)
	if !strings.EqualFold(info.get("Metadata Stream"), "yes") {
		return md, nil
	}

	rawXMP, err := runInfo(ctx, data, size, tools.Info, "-meta", "-")
	if err != nil || strings.TrimSpace(rawXMP) == "" {
		return md, nil
	}
	xmp, err := parseXMP(rawXMP)
	if err != nil {
		return md, nil
	}
	return mergeMetadata(md, xmp), nil
}

// mergeMetadata applies Calibre's PDF rule: XMP wins unless the Info ModDate
// is newer than the XMP metadata date. Field by field, the preferred source
// wins when it has a value, otherwise the other source fills the gap.
func mergeMetadata(info, xmp metadata) metadata {
	preferInfo := !info.modDate.IsZero() && !xmp.xmpDate.IsZero() && info.modDate.After(xmp.xmpDate)

	pickString := func(infoValue, xmpValue string) string {
		if preferInfo {
			if infoValue != "" {
				return infoValue
			}
			return xmpValue
		}
		if xmpValue != "" {
			return xmpValue
		}
		return infoValue
	}
	pickStrings := func(infoValues, xmpValues []string) []string {
		if preferInfo {
			if len(infoValues) > 0 {
				return infoValues
			}
			return xmpValues
		}
		if len(xmpValues) > 0 {
			return xmpValues
		}
		return infoValues
	}

	return metadata{
		title:           pickString(info.title, xmp.title),
		authors:         pickStrings(info.authors, xmp.authors),
		description:     pickString(info.description, xmp.description),
		subjects:        pickStrings(info.subjects, xmp.subjects),
		publisher:       pickString(info.publisher, xmp.publisher),
		publicationDate: pickString(info.publicationDate, xmp.publicationDate),
		series:          pickString(info.series, xmp.series),
		seriesIndex:     pickString(info.seriesIndex, xmp.seriesIndex),
		languages:       pickStrings(info.languages, xmp.languages),
		identifiers:     mergeIdentifierLists(info.identifiers, xmp.identifiers),
		modDate:         info.modDate,
		xmpDate:         xmp.xmpDate,
	}
}

// applyMetadata overlays the PDF metadata on the text-derived values. The PDF
// metadata wins for fields it carries; the text fallback is kept otherwise.
func applyMetadata(md *parser.Metadata, meta metadata) {
	if meta.title != "" {
		md.Title = meta.title
	}
	if len(meta.authors) > 0 {
		md.Authors = textdoc.FilterMetadataAuthors(meta.authors, md.Title)
	}
	if meta.description != "" {
		md.Description = meta.description
	}
	if len(meta.subjects) > 0 {
		md.Subjects = meta.subjects
	}
	if meta.publisher != "" {
		md.Publisher = meta.publisher
	}
	if meta.publicationDate != "" {
		md.PublicationDate = meta.publicationDate
	}
	if meta.series != "" {
		md.Series = meta.series
	}
	if meta.seriesIndex != "" {
		md.SeriesIndex = meta.seriesIndex
	}
	if len(meta.languages) > 0 {
		md.Languages = append([]string(nil), meta.languages...)
	}
	if len(meta.identifiers) > 0 {
		md.Identifiers = mergeIdentifierLists(md.Identifiers, meta.identifiers)
	}
}

// metadataClaims turns the raw PDF Info/XMP metadata into a claim set. The
// text fallback and applyMetadata precedence intentionally stay out of it.
func metadataClaims(meta metadata) []claim.Set {
	if meta.title == "" && len(meta.authors) == 0 {
		return nil
	}
	return []claim.Set{{
		Key:     "metadata",
		Source:  claim.SourceMetadata,
		Origin:  "pdf",
		Title:   meta.title,
		Authors: meta.authors,
		Trust:   claim.TrustStrong,
		Witness: claim.WitnessIndependent,
	}}
}

// xmpNode is one element of the XMP XML tree.
type xmpNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     strings.Builder
	children []*xmpNode
}

// parseXMP parses an XMP packet into the metadata fields we support. The
// packet may be prefixed by a legacy "Metadata:" line emitted by older
// Poppler versions.
func parseXMP(raw string) (metadata, error) {
	root, err := parseXMPTree(raw)
	if err != nil {
		return metadata{}, err
	}

	md := metadata{
		title:       cleanTitle(firstAlt(textByLocal(root, "title"))),
		authors:     cleanAuthors(textValues(root, "creator")),
		description: cleanOptional(firstAlt(textByLocal(root, "description"))),
		subjects:    xmpSubjects(root),
		publisher:   cleanOptional(firstValue(root, "publisher")),
		languages:   cleanLanguages(textValues(root, "language")),
		series:      cleanOptional(firstResourceValue(root, "series")),
		seriesIndex: cleanValue(firstDescendantText(root, "series_index")),
		identifiers: xmpIdentifiers(root),
	}
	md.publicationDate = normalizeDate(firstValue(root, "date"))
	if md.publicationDate == "" {
		md.publicationDate = normalizeDate(firstValue(root, "CreateDate"))
	}
	md.xmpDate = parsePDFDate(firstValue(root, "MetadataDate"))
	if md.xmpDate.IsZero() {
		md.xmpDate = parsePDFDate(firstValue(root, "ModifyDate"))
	}
	return md, nil
}

func parseXMPTree(raw string) (*xmpNode, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "Metadata:") {
		raw = strings.TrimSpace(raw[len("Metadata:"):])
	}
	if raw == "" {
		return nil, errors.New("pdf: empty XMP packet")
	}

	decoder := xml.NewDecoder(strings.NewReader(raw))
	decoder.CharsetReader = charset.NewReaderLabel
	var root *xmpNode
	var stack []*xmpNode
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pdf: parse XMP: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			node := &xmpNode{name: token.Name, attrs: append([]xml.Attr(nil), token.Attr...)}
			if len(stack) == 0 {
				if root == nil {
					root = node
				}
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(token)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root == nil {
		return nil, errors.New("pdf: empty XMP packet")
	}
	return root, nil
}

// allNodes returns every element in depth-first order.
func allNodes(root *xmpNode) []*xmpNode {
	if root == nil {
		return nil
	}
	out := []*xmpNode{root}
	for _, child := range root.children {
		out = append(out, allNodes(child)...)
	}
	return out
}

// nodesByLocal returns all elements with the given local name.
func nodesByLocal(root *xmpNode, local string) []*xmpNode {
	var out []*xmpNode
	for _, node := range allNodes(root) {
		if node.name.Local == local {
			out = append(out, node)
		}
	}
	return out
}

// textByLocal returns the nodes with the given local name, retaining their
// full text content. It is a small wrapper used by parseXMP.
func textByLocal(root *xmpNode, local string) []*xmpNode {
	return nodesByLocal(root, local)
}

// textValues returns the rdf:li values below every matching element. When an
// element is a simple property, its own text is returned instead.
func textValues(root *xmpNode, local string) []string {
	nodes := nodesByLocal(root, local)
	if len(nodes) == 0 {
		return nil
	}
	var out []string
	for _, node := range nodes {
		out = append(out, listValues(node)...)
	}
	return out
}

// listValues returns all rdf:li values below n, or n's text when it has no
// list items.
func listValues(n *xmpNode) []string {
	lis := nodesByLocal(n, "li")
	if len(lis) == 0 {
		if value := cleanValue(n.content()); value != "" {
			return []string{value}
		}
		return nil
	}
	out := make([]string, 0, len(lis))
	for _, li := range lis {
		if value := cleanValue(li.content()); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// firstAlt returns the x-default language alternative, or the first value.
func firstAlt(nodes []*xmpNode) string {
	if len(nodes) == 0 {
		return ""
	}
	lis := nodesByLocal(nodes[0], "li")
	for _, li := range lis {
		if language := attr(li, "lang"); language == "x-default" {
			return cleanValue(li.content())
		}
	}
	if len(lis) > 0 {
		return cleanValue(lis[0].content())
	}
	return cleanValue(nodes[0].content())
}

func firstValue(root *xmpNode, local string) string {
	nodes := nodesByLocal(root, local)
	if len(nodes) == 0 {
		return ""
	}
	values := listValues(nodes[0])
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstDescendantText(root *xmpNode, local string) string {
	nodes := nodesByLocal(root, local)
	if len(nodes) == 0 {
		return ""
	}
	return nodes[0].content()
}

// firstResourceValue reads the rdf:value property inside a Calibre series
// resource.
func firstResourceValue(root *xmpNode, local string) string {
	for _, node := range nodesByLocal(root, local) {
		if value := firstDescendantText(node, "value"); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// xmpIdentifiers scans the identifier-like XMP properties for validated ISBNs
// and DOIs.
func xmpIdentifiers(root *xmpNode) []identifier.Identifier {
	var values []string
	for _, node := range allNodes(root) {
		switch strings.ToLower(node.name.Local) {
		case "identifier", "isbn", "doi":
			values = append(values, decodePDFText(node.content()))
		}
	}
	return identifiersFromTexts(values)
}

func (n *xmpNode) content() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(n.text.String())
	for _, child := range n.children {
		b.WriteString(child.content())
	}
	return b.String()
}

func attr(n *xmpNode, local string) string {
	for _, a := range n.attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// identifiersFromTexts extracts validated ISBN-13s and DOIs from metadata
// values and marks them as metadata-derived.
func identifiersFromTexts(values []string) []identifier.Identifier {
	var out []identifier.Identifier
	seen := make(map[string]struct{})
	add := func(item identifier.Identifier) {
		key := strings.ToUpper(item.Scheme) + "\x00" + item.Value
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	for _, value := range values {
		for _, isbn := range tools.FindISBNs(value) {
			isbn13, ok := isbn.ToISBN13()
			if !ok {
				continue
			}
			add(identifier.Identifier{Scheme: "ISBN", Value: isbn13.String(), Source: identifier.SourceMetadata})
		}
		for _, doi := range doiPattern.FindAllString(value, -1) {
			doi = strings.TrimRight(doi, ".,;:)]}")
			if doi == "" {
				continue
			}
			add(identifier.Identifier{Scheme: "DOI", Value: strings.ToLower(doi), Source: identifier.SourceMetadata})
		}
	}
	return out
}

// mergeIdentifierLists combines two identifier lists, merging provenance for
// duplicate scheme/value pairs.
func mergeIdentifierLists(a, b []identifier.Identifier) []identifier.Identifier {
	out := make([]identifier.Identifier, 0, len(a)+len(b))
	index := make(map[string]int, len(a)+len(b))
	add := func(item identifier.Identifier) {
		key := strings.ToUpper(item.Scheme) + "\x00" + item.Value
		if i, ok := index[key]; ok {
			out[i].Source = identifier.MergeSources(out[i].Source, item.Source)
			return
		}
		index[key] = len(out)
		out = append(out, item)
	}
	for _, item := range a {
		add(item)
	}
	for _, item := range b {
		add(item)
	}
	return out
}

var doiPattern = regexp.MustCompile(`(?i)\b10\.\d{4,9}/[-._;()/:a-z0-9]+\b`)

// cleanTitle applies the shared title cleaning rules.
func cleanTitle(value string) string {
	return cleanOptional(value)
}

func cleanValue(value string) string {
	return textdoc.CleanValue(decodePDFText(value))
}

// cleanOptional applies the placeholder and junk filters to a non-title
// metadata value.
func cleanOptional(value string) string {
	value = cleanValue(value)
	if value == "" || textdoc.IsPlaceholder(value) || metaheur.IsJunkValue(value) {
		return ""
	}
	return value
}

func cleanAuthors(authors []string) []string {
	var out []string
	for _, author := range authors {
		out = append(out, textdoc.SplitAuthors(decodePDFText(author))...)
	}
	return textdoc.FilterMetadataAuthors(out, "")
}

func splitSubjects(raw string) []string {
	raw = decodePDFText(raw)
	var out []string
	seen := make(map[string]struct{})
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';'
	}) {
		value := cleanValue(part)
		if value == "" || textdoc.IsPlaceholder(value) || metaheur.IsJunkValue(value) || isISBNSubject(value) {
			continue
		}
		key := metaheur.Fold(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanSubjects(subjects []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, subject := range subjects {
		value := cleanValue(subject)
		if value == "" || textdoc.IsPlaceholder(value) || metaheur.IsJunkValue(value) || isISBNSubject(value) {
			continue
		}
		key := metaheur.Fold(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isISBNSubject(value string) bool {
	if _, ok := tools.NormalizeISBN(value); ok {
		return true
	}
	return len(tools.FindISBNs(value)) > 0
}

// xmpSubjects prefers dc:subject and falls back to pdf:Keywords, which is a
// single comma/semicolon separated property rather than an rdf list.
func xmpSubjects(root *xmpNode) []string {
	if subjects := cleanSubjects(textValues(root, "subject")); len(subjects) > 0 {
		return subjects
	}
	var out []string
	for _, value := range textValues(root, "Keywords") {
		out = append(out, splitSubjects(value)...)
	}
	return out
}

func cleanLanguages(languages []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, language := range languages {
		value := strings.ToLower(cleanValue(language))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// normalizeDate keeps a valid date in YYYY-MM-DD form and leaves anything
// else untouched.
func normalizeDate(value string) string {
	if value = cleanValue(value); value == "" {
		return ""
	}
	if parsed := parsePDFDate(value); !parsed.IsZero() {
		return parsed.Format("2006-01-02")
	}
	return value
}

var pdfDateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05-07",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"2006:01:02 15:04:05",
}

func parsePDFDate(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range pdfDateLayouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
