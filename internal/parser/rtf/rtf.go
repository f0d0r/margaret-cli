// Package rtf extracts plain text and metadata from RTF documents.
package rtf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/f0d0r/margaret-cli/internal/langid"
	"github.com/f0d0r/margaret-cli/internal/metaheur"
	"github.com/f0d0r/margaret-cli/internal/parser"
	"github.com/f0d0r/margaret-cli/internal/parser/textdoc"
	"github.com/f0d0r/margaret-ebook-library/book"
	"github.com/f0d0r/margaret-ebook-library/tools"
)

const (
	// headLimit is how much decoded text is kept for metadata, ISBN and
	// language extraction.
	headLimit = 64 << 10
	// signatureWindow is enough for a BOM, leading whitespace and "{\rtf".
	signatureWindow = 32
)

var (
	// ErrNotRTF reports a file that does not start with an RTF header.
	ErrNotRTF = errors.New("rtf: not an RTF document")
	utf8BOM   = []byte{0xEF, 0xBB, 0xBF}
)

// RtfParser extracts metadata from RTF books. The body text is decoded to
// plain text and fed to the same fingerprint, ISBN and language pipeline as
// the plain-text parser. Title, authors and the other metadata fields come
// from the RTF \info block, with the shared Calibre-style title-page fallback
// for documents that carry no \info metadata.
type RtfParser struct {
	// MinBookSize is the minimum size in bytes for an RTF file to be treated
	// as a book. Zero uses parser.DefaultMinDocumentSize; a negative value
	// disables the size filter.
	MinBookSize int64
}

var (
	_ parser.Parser     = RtfParser{}
	_ parser.SizeFilter = RtfParser{}
)

// MinSize implements parser.SizeFilter.
func (p RtfParser) MinSize() int64 {
	return parser.ResolveMinSize(p.MinBookSize)
}

// Parse decodes the RTF body to plain text and returns its metadata, content
// hash and MinHash fingerprint. The raw bytes are read once: the content hash
// and the decoded text stream are produced in the same pass. Files without an
// RTF header are rejected; documents without textual content are reported as
// not a book.
func (p RtfParser) Parse(ctx context.Context, ebookData book.Blob) (parser.Metadata, error) {
	if ebookData == nil {
		return parser.Metadata{}, errors.New("rtf: nil blob")
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}
	data := parser.WithContext(ctx, ebookData)

	size, err := data.Size()
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("rtf: file size: %w", err))
	}
	if size < 0 {
		return parser.Metadata{}, fmt.Errorf("rtf: invalid size %d", size)
	}
	if min := p.MinSize(); min > 0 && size < min {
		return parser.Metadata{}, parser.ErrNotABook
	}
	if size == 0 {
		return parser.Metadata{}, ErrNotRTF
	}
	ok, err := hasRTFHeader(data, size)
	if err != nil {
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("rtf: read header: %w", err))
	}
	if !ok {
		return parser.Metadata{}, ErrNotRTF
	}

	// The raw bytes are hashed and decoded in one pass: the decoder consumes
	// the whole section for the fingerprint, so the tee sees the whole file.
	rawHash := sha256.New()
	decoder := newTextDecoder(io.TeeReader(io.NewSectionReader(data, 0, size), rawHash))
	gate := textdoc.NewHeadGate(headLimit, size, func(head string, size int64) error {
		if strings.TrimSpace(head) == "" || textdoc.IsJunkContent(head, size) {
			return parser.ErrNotABook
		}
		return nil
	})
	fingerprint, err := tools.FingerprintReader(io.TeeReader(decoder, gate),
		tools.WithMinHash(tools.MinHashConfig{NumHashes: 128, ShingleSize: 5}))
	if err != nil {
		if errors.Is(err, parser.ErrNotABook) {
			return parser.Metadata{}, parser.ErrNotABook
		}
		return parser.Metadata{}, parser.ContextError(ctx, fmt.Errorf("rtf: read text: %w", err))
	}
	if err := gate.Finish(); err != nil {
		return parser.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return parser.Metadata{}, err
	}

	head := gate.Head()
	md := textdoc.ExtractMetadata(head)
	applyInfo(&md, decoder.infoValues())
	detected, detectedOK := langid.DetectProse(head)
	md.Languages = parser.ApplyDetectedLanguage(md.Languages, detected, detectedOK)
	md.Hash = hex.EncodeToString(rawHash.Sum(nil))
	md.MinHash = fingerprint.MinHash
	md.Identifiers = textdoc.TextIdentifiers(head)
	return md, nil
}

// infoValues returns the collected \info fields with surrounding whitespace
// collapsed.
func (d *textDecoder) infoValues() map[string]string {
	out := make(map[string]string, len(d.info))
	for name, builder := range d.info {
		if value := textdoc.CleanValue(builder.String()); value != "" {
			out[name] = value
		}
	}
	return out
}

// hasRTFHeader reports whether the file starts with "{\rtf" after an optional
// BOM and whitespace.
func hasRTFHeader(b book.Blob, size int64) (bool, error) {
	n := min(size, int64(signatureWindow))
	buf := make([]byte, n)
	if _, err := b.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	buf = bytes.TrimLeft(buf, " \t\r\n")
	buf = bytes.TrimPrefix(buf, utf8BOM)
	buf = bytes.TrimLeft(buf, " \t\r\n")
	return bytes.HasPrefix(buf, []byte(`{\rtf`)), nil
}

// applyInfo overlays the RTF \info block on the text-derived metadata. The
// field mapping follows Calibre: \subject is the description, \category the
// subjects and \manager the publisher.
func applyInfo(md *parser.Metadata, info map[string]string) {
	if title := infoValue(info["title"]); title != "" {
		md.Title = title
	}
	if authors := infoAuthors(info["author"]); len(authors) > 0 {
		md.Authors = authors
	}
	description := infoValue(info["subject"])
	if description == "" {
		description = infoValue(info["doccomm"])
	}
	if description == "" {
		description = infoValue(info["comment"])
	}
	if description != "" {
		md.Description = description
	}
	if subjects := infoSubjects(info["category"], info["keywords"]); len(subjects) > 0 {
		md.Subjects = subjects
	}
	publisher := infoValue(info["manager"])
	if publisher == "" {
		publisher = infoValue(info["company"])
	}
	if publisher != "" {
		md.Publisher = publisher
	}
}

func infoValue(raw string) string {
	value := textdoc.CleanValue(raw)
	if value == "" || textdoc.IsPlaceholder(value) || metaheur.IsJunkValue(value) {
		return ""
	}
	return value
}

// infoAuthors splits the \author value on commas like Calibre, then applies
// the same author cleaning and plausibility filter as the text parser.
func infoAuthors(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var authors []string
	for _, part := range strings.Split(raw, ",") {
		authors = append(authors, textdoc.SplitAuthors(part)...)
	}
	return textdoc.FilterMetadataAuthors(authors, "")
}

func infoSubjects(category, keywords string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, raw := range []string{category, keywords} {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';'
		}) {
			value := infoValue(part)
			if value == "" {
				continue
			}
			key := metaheur.Fold(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}
