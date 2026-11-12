package pdf

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// decodePDFText decodes strings that Poppler emits from PDF text strings as
// literal backslash-octal escapes. XMP values are the common case: a
// UTF-16BE title is printed as \376\377\000J\000e... instead of being
// transcoded. PDFDocEncoding strings (for example \341) are also decoded.
func decodePDFText(s string) string {
	if s == "" || !looksPDFEscaped(s) {
		return s
	}
	decoded := decodePDFBytes(unescapePDFString(s))
	if strings.IndexByte(decoded, 0) >= 0 || strings.ContainsRune(decoded, utf8.RuneError) {
		// The escaped value was corrupt; let the caller fall back to the
		// other metadata source instead of storing binary noise.
		return ""
	}
	return decoded
}

// looksPDFEscaped keeps ordinary backslashes (Windows paths, prose) intact.
// Only octal escapes and the UTF-16 BOM escapes trigger decoding.
func looksPDFEscaped(s string) bool {
	if strings.IndexByte(s, 0) >= 0 {
		return true
	}
	if strings.Contains(s, `\376\377`) || strings.Contains(s, `\377\376`) {
		return true
	}
	for i := 0; i+3 < len(s); i++ {
		if s[i] == '\\' && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			return true
		}
	}
	return false
}

func isOctal(b byte) bool {
	return b >= '0' && b <= '7'
}

// unescapePDFString decodes PDF string escapes into bytes. Unknown escapes
// keep the escaped character, matching the PDF specification.
func unescapePDFString(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out = append(out, s[i])
			continue
		}
		if i+1 >= len(s) {
			out = append(out, '\\')
			break
		}
		i++
		switch s[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '(', ')', '\\':
			out = append(out, s[i])
		default:
			if isOctal(s[i]) {
				value, digits := 0, 0
				for digits < 3 && i < len(s) && isOctal(s[i]) {
					value = value*8 + int(s[i]-'0')
					i++
					digits++
				}
				i--
				out = append(out, byte(value))
				continue
			}
			out = append(out, s[i])
		}
	}
	return out
}

func decodePDFBytes(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeUTF16(b[2:], true)
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeUTF16(b[2:], false)
	}
	if little, ok := utf16NULPattern(b); ok {
		return decodeUTF16(b, !little)
	}
	if utf8.Valid(b) {
		return string(b)
	}
	// PDFDocEncoding is Latin-1-like for the common accented letters; the
	// Windows-1252 fallback maps those bytes correctly.
	if decoded, err := charmap.Windows1252.NewDecoder().Bytes(b); err == nil {
		return string(decoded)
	}
	return string(b)
}

// utf16NULPattern detects BOM-less UTF-16 by the NUL bytes in ASCII-heavy
// text. A BOM always wins; this only covers the rarer escaped form.
func utf16NULPattern(b []byte) (little bool, ok bool) {
	if len(b) < 4 {
		return false, false
	}
	pairs := len(b) / 2
	evenNUL, oddNUL := 0, 0
	for i := 0; i+1 < len(b); i += 2 {
		if b[i] == 0 {
			evenNUL++
		}
		if b[i+1] == 0 {
			oddNUL++
		}
	}
	half := (pairs + 1) / 2
	if evenNUL > 0 && evenNUL >= half && evenNUL > oddNUL*2 {
		return false, true
	}
	if oddNUL > 0 && oddNUL >= half && oddNUL > evenNUL*2 {
		return true, true
	}
	return false, false
}

func decodeUTF16(b []byte, bigEndian bool) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return strings.TrimRight(string(utf16.Decode(units)), "\x00")
}
