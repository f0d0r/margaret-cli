package rtf

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// destinationSkipped reports RTF destinations whose content is not book text.
func destinationSkipped(name string) bool {
	switch name {
	case "annotation", "atndate", "atnauthor", "atnid", "atnref",
		"bkmkend", "bkmkstart", "colorschememapping", "colortbl",
		"datastore", "docvar", "filetbl", "fonttbl", "footer", "footerf",
		"footerl", "footerr", "fldinst", "ftnsep", "ftnsepc", "aftnsep",
		"aftnsepc", "generator", "header", "headerf", "headerl", "headerr",
		"latentstyles", "listtable", "listoverridetable", "listtext",
		"mmath", "mmathPr", "nonshppict", "objclass", "objdata", "objname",
		"object", "pict", "pn", "pnseclvl", "pntext", "propname", "rsidtbl",
		"shp", "shpinst", "shprslt", "staticval", "stylesheet",
		"themedata", "tc", "tcn", "template", "userprops", "xmlclose",
		"xmlnstbl", "xmlopen", "xe":
		return true
	default:
		return false
	}
}

// infoField reports an RTF \info member that can carry book metadata.
func infoField(name string) bool {
	switch name {
	case "author", "category", "comment", "company", "doccomm", "keywords",
		"manager", "operator", "subject", "title":
		return true
	default:
		return false
	}
}

// groupState is the state of one RTF group.
type groupState struct {
	uc        int
	font      int
	skip      bool
	hidden    bool
	dest      string
	infoField string
}

// textDecoder turns RTF bytes into a UTF-8 plain-text stream. It handles only
// the parts of RTF a book parser needs: group scoping, destinations, text
// escapes and paragraph commands. The first 64 KiB of emitted text and the
// contents of the \info block are all callers need from a header pass.
type textDecoder struct {
	br          *bufio.Reader
	enc         encoding.Encoding
	defaultFont int
	fonts       map[int]encoding.Encoding
	pending     []byte
	out         []byte
	stack       []groupState
	skipChars   int
	highRune    rune
	info        map[string]*strings.Builder
	infoStarted map[string]bool
	bomChecked  bool
	done        bool
}

func newTextDecoder(r io.Reader) *textDecoder {
	d := &textDecoder{
		br:          bufio.NewReaderSize(r, 32<<10),
		enc:         charmap.Windows1252,
		stack:       []groupState{{uc: 1}},
		info:        make(map[string]*strings.Builder),
		infoStarted: make(map[string]bool),
	}
	return d
}

// Read implements io.Reader over the decoded text.
func (d *textDecoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(d.out) == 0 {
		if d.done {
			return 0, io.EOF
		}
		err := d.step()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return 0, err
			}
			d.done = true
			d.finish()
			if len(d.out) == 0 {
				return 0, io.EOF
			}
			break
		}
	}
	n := copy(p, d.out)
	if n == len(d.out) {
		d.out = d.out[:0]
	} else {
		d.out = d.out[n:]
	}
	return n, nil
}

// buffered returns the bytes the reader holds without consuming them. It fills
// the buffer only when it is empty: peeking the full size on every step would
// make bufio copy the remaining buffer and read a single byte at a time.
func (d *textDecoder) buffered() ([]byte, error) {
	if d.br.Buffered() == 0 {
		if _, err := d.br.Peek(1); err != nil {
			return nil, err
		}
	}
	buf, _ := d.br.Peek(d.br.Buffered())
	return buf, nil
}

// step consumes input until a control sequence is handled or text is emitted.
// Ordinary text and skipped payloads are consumed in runs; only the bytes that
// change the decoder state go through the per-byte path.
func (d *textDecoder) step() error {
	buf, err := d.buffered()
	if len(buf) == 0 {
		if err == nil {
			err = io.EOF
		}
		return err
	}
	if !d.bomChecked {
		if bytes.HasPrefix(buf, utf8BOM) {
			_, _ = d.br.Discard(len(utf8BOM))
			d.bomChecked = true
			return nil
		}
		if buf[0] >= 0x20 {
			d.bomChecked = true
		}
	}
	switch b := buf[0]; {
	case b == '{':
		_, _ = d.br.Discard(1)
		d.flushPending()
		d.push()
	case b == '}':
		_, _ = d.br.Discard(1)
		d.flushPending()
		d.pop()
	case b == '\\':
		_, _ = d.br.Discard(1)
		return d.control()
	case b < 0x20:
		// Source formatting whitespace is not document text.
		n := 1
		for n < len(buf) && buf[n] < 0x20 {
			n++
		}
		_, _ = d.br.Discard(n)
	case b >= 0x80:
		d.highRun(buf)
	case d.skipChars > 0:
		_, _ = d.br.Discard(1)
		d.skipChars--
	default:
		d.plainRun(buf)
	}
	return nil
}

// plainRun consumes the leading run of ordinary text bytes and writes it in
// one step. buf[0] is known to be an ordinary byte and skipChars is zero.
func (d *textDecoder) plainRun(buf []byte) {
	n := 1
	for n < len(buf) {
		c := buf[n]
		if c < 0x20 || c >= 0x80 || c == '\\' || c == '{' || c == '}' {
			break
		}
		n++
	}
	d.flushPending()
	d.flushHighRune()
	run := buf[:n]
	if builder := d.infoBuilder(); builder != nil {
		builder.Write(run)
	}
	if !d.state().skip && !d.state().hidden {
		d.out = append(d.out, run...)
	}
	_, _ = d.br.Discard(n)
}

// highRun consumes the leading run of bytes above ASCII and buffers them for
// the active code page. buf[0] is known to be a high byte.
func (d *textDecoder) highRun(buf []byte) {
	n := 1
	for n < len(buf) && buf[n] >= 0x80 {
		n++
	}
	run := buf[:n]
	if drop := min(d.skipChars, n); drop > 0 {
		d.skipChars -= drop
		run = run[drop:]
	}
	if len(run) > 0 && (d.infoBuilder() != nil || (!d.state().skip && !d.state().hidden)) {
		d.pending = append(d.pending, run...)
	}
	_, _ = d.br.Discard(n)
}

func (d *textDecoder) control() error {
	b, err := d.br.ReadByte()
	if err != nil {
		return err
	}
	if b == '\'' {
		return d.hexEscape()
	}
	if !isASCIILetter(b) {
		d.controlSymbol(b)
		return nil
	}

	var name strings.Builder
	for isASCIILetter(b) {
		name.WriteByte(b)
		b, err = d.br.ReadByte()
		if err != nil {
			return d.controlWord(name.String(), 0, false)
		}
	}
	param, hasParam := 0, false
	sign := 1
	if b == '-' {
		sign = -1
		b, err = d.br.ReadByte()
		if err != nil {
			return d.controlWord(name.String(), 0, false)
		}
	}
	for isASCIIDigit(b) {
		param = param*10 + int(b-'0')
		hasParam = true
		b, err = d.br.ReadByte()
		if err != nil {
			return d.controlWord(name.String(), sign*param, hasParam)
		}
	}
	if b != ' ' {
		if err := d.br.UnreadByte(); err != nil {
			return err
		}
	}
	return d.controlWord(name.String(), sign*param, hasParam)
}

func (d *textDecoder) controlSymbol(b byte) {
	if d.skipChars > 0 {
		switch b {
		case '\\', '{', '}', '~', '_', '-', '\r', '\n':
			d.skipChars--
			return
		}
	}
	switch b {
	case '\\', '{', '}':
		d.emitString(string(b))
	case '~':
		d.emitString("\u00a0")
	case '_':
		d.emitString("-")
	case '-', ':', '|', ';', '?', '!', '>', '<', '=':
		// Optional hyphen, formula and annotation separators carry no text.
	case '\r', '\n':
		d.emitString("\n")
	case '*':
		d.flushPending()
		if len(d.stack) > 1 {
			state := &d.stack[len(d.stack)-1]
			state.skip = true
			state.dest = "*"
		}
	}
}

func (d *textDecoder) hexEscape() error {
	hi, err := d.br.ReadByte()
	if err != nil {
		return err
	}
	lo, err := d.br.ReadByte()
	if err != nil {
		return err
	}
	value, ok := hexValue(hi, lo)
	if !ok {
		return nil
	}
	if d.skipChars > 0 {
		d.skipChars--
		return nil
	}
	d.pending = append(d.pending, value)
	return nil
}

func (d *textDecoder) controlWord(name string, param int, hasParam bool) error {
	switch name {
	case "ansicpg":
		if hasParam {
			d.flushPending()
			d.enc = encodingForCodePage(param)
		}
	case "deff":
		if hasParam {
			d.flushPending()
			d.defaultFont = param
			d.state().font = param
		}
	case "f":
		if hasParam && d.state().font != param {
			d.flushPending()
			d.state().font = param
		}
	case "fcharset":
		if hasParam && d.state().dest == "fonttbl" && d.state().font >= 0 {
			if codePage := fontCharsetCodePages[param]; codePage != 0 {
				if d.fonts == nil {
					d.fonts = make(map[int]encoding.Encoding)
				}
				d.fonts[d.state().font] = encodingForCodePage(codePage)
			}
		}
	case "plain":
		if d.state().font != d.defaultFont {
			d.flushPending()
			d.state().font = d.defaultFont
		}
	case "uc":
		if hasParam {
			d.stack[len(d.stack)-1].uc = param
			d.skipChars = 0
		}
	case "u":
		if hasParam {
			d.unicode(param)
		}
	case "bin":
		if hasParam && param > 0 {
			if d.skipChars > 0 {
				d.skipChars--
			}
			if _, err := io.CopyN(io.Discard, d.br, int64(param)); err != nil &&
				!errors.Is(err, io.EOF) {
				return err
			}
		}
	case "par", "line", "sect", "page", "softpage", "softline":
		d.emitString("\n")
	case "tab":
		d.emitString("\t")
	case "cell":
		d.emitString("\t")
	case "row":
		d.emitString("\n")
	case "emdash":
		d.emitString("\u2014")
	case "endash":
		d.emitString("\u2013")
	case "bullet":
		d.emitString("\u2022")
	case "lquote":
		d.emitString("\u2018")
	case "rquote":
		d.emitString("\u2019")
	case "ldblquote":
		d.emitString("\u201c")
	case "rdblquote":
		d.emitString("\u201d")
	case "emspace", "enspace", "qmspace":
		d.emitString(" ")
	case "v":
		d.flushPending()
		state := &d.stack[len(d.stack)-1]
		state.hidden = !hasParam || param != 0
	default:
		if d.inInfo() && infoField(name) {
			d.startInfoField(name)
			return nil
		}
		if destinationSkipped(name) && len(d.stack) > 1 {
			d.flushPending()
			state := &d.stack[len(d.stack)-1]
			state.skip = true
			state.dest = name
			if name == "fonttbl" {
				state.font = -1
			}
			return nil
		}
		if name == "info" && len(d.stack) > 1 {
			d.flushPending()
			state := &d.stack[len(d.stack)-1]
			state.skip = true
			state.dest = "info"
		}
	}
	return nil
}

// unicode handles a \uN control word, pairing UTF-16 surrogates and arming the
// \ucN fallback-character skip.
func (d *textDecoder) unicode(value int) {
	d.flushPending()
	r := rune(value)
	if r < 0 {
		r += 65536
	}
	switch {
	case utf16.IsSurrogate(r) && r >= 0xD800 && r <= 0xDBFF:
		d.flushHighRune()
		d.highRune = r
	case utf16.IsSurrogate(r) && d.highRune != 0:
		decoded := utf16.DecodeRune(d.highRune, r)
		d.highRune = 0
		if decoded == utf8.RuneError {
			d.writeString(string(utf8.RuneError))
		} else {
			d.writeString(string(decoded))
		}
	default:
		d.flushHighRune()
		d.writeString(string(r))
	}
	d.skipChars = d.stack[len(d.stack)-1].uc
}

func (d *textDecoder) startInfoField(name string) {
	if d.infoStarted[name] {
		d.stack[len(d.stack)-1].infoField = ""
		return
	}
	d.infoStarted[name] = true
	d.info[name] = &strings.Builder{}
	d.stack[len(d.stack)-1].infoField = name
}

func (d *textDecoder) push() {
	parent := d.stack[len(d.stack)-1]
	d.stack = append(d.stack, groupState{
		uc:        parent.uc,
		font:      parent.font,
		skip:      parent.skip,
		hidden:    parent.hidden,
		dest:      parent.dest,
		infoField: parent.infoField,
	})
	d.skipChars = 0
}

func (d *textDecoder) pop() {
	if len(d.stack) == 1 {
		return
	}
	d.flushPending()
	d.stack = d.stack[:len(d.stack)-1]
	d.skipChars = 0
}

func (d *textDecoder) inInfo() bool {
	for i := len(d.stack) - 1; i >= 0; i-- {
		switch dest := d.stack[i].dest; dest {
		case "":
			continue
		case "info":
			return true
		default:
			return false
		}
	}
	return false
}

func (d *textDecoder) state() *groupState {
	return &d.stack[len(d.stack)-1]
}

// infoBuilder returns the builder of the \info field being captured, or nil.
func (d *textDecoder) infoBuilder() *strings.Builder {
	field := d.state().infoField
	if field == "" || !d.inInfo() {
		return nil
	}
	return d.info[field]
}

func (d *textDecoder) emitString(s string) {
	d.flushPending()
	d.flushHighRune()
	d.writeString(s)
}

func (d *textDecoder) writeString(s string) {
	if len(s) == 0 {
		return
	}
	if builder := d.infoBuilder(); builder != nil {
		builder.WriteString(s)
	}
	if d.state().skip || d.state().hidden {
		return
	}
	d.out = append(d.out, s...)
}

func (d *textDecoder) writeBytes(b []byte) {
	if len(b) == 0 {
		return
	}
	if builder := d.infoBuilder(); builder != nil {
		builder.Write(b)
	}
	if d.state().skip || d.state().hidden {
		return
	}
	d.out = append(d.out, b...)
}

func (d *textDecoder) flushPending() {
	if len(d.pending) == 0 {
		return
	}
	raw := d.pending
	d.pending = d.pending[:0]
	enc := d.enc
	if fontEncoding := d.fonts[d.state().font]; fontEncoding != nil {
		enc = fontEncoding
	}
	decoded, _, err := transform.Bytes(enc.NewDecoder(), raw)
	if err != nil {
		decoded, _, _ = transform.Bytes(charmap.Windows1252.NewDecoder(), raw)
	}
	d.writeBytes(decoded)
}

func (d *textDecoder) flushHighRune() {
	if d.highRune == 0 {
		return
	}
	d.highRune = 0
	d.writeString(string(utf8.RuneError))
}

func (d *textDecoder) finish() {
	d.flushPending()
	d.flushHighRune()
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func hexValue(hi, lo byte) (byte, bool) {
	h, okH := hexDigit(hi)
	l, okL := hexDigit(lo)
	if !okH || !okL {
		return 0, false
	}
	return h<<4 | l, true
}

func hexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
