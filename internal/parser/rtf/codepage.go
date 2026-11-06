package rtf

import (
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// Unmapped RTF font charsets use the document code page.
var fontCharsetCodePages = map[int]int{
	77:  10000, // Macintosh
	128: 932,   // Shift-JIS
	129: 949,   // Korean
	134: 936,   // Simplified Chinese
	136: 950,   // Traditional Chinese
	161: 1253,  // Greek
	162: 1254,  // Turkish
	163: 1258,  // Vietnamese
	177: 1255,  // Hebrew
	178: 1256,  // Arabic
	186: 1257,  // Baltic
	204: 1251,  // Cyrillic
	222: 874,   // Thai
	238: 1250,  // Central European
}

// encodingForCodePage maps an RTF \ansicpg number to a decoder. Unknown code
// pages fall back to Windows-1252, the RTF default.
func encodingForCodePage(codePage int) encoding.Encoding {
	switch codePage {
	case 0, 1252:
		return charmap.Windows1252
	case 437:
		return charmap.CodePage437
	case 850:
		return charmap.CodePage850
	case 852:
		return charmap.CodePage852
	case 855:
		return charmap.CodePage855
	case 858:
		return charmap.CodePage858
	case 860:
		return charmap.CodePage860
	case 862:
		return charmap.CodePage862
	case 863:
		return charmap.CodePage863
	case 865:
		return charmap.CodePage865
	case 866:
		return charmap.CodePage866
	case 874:
		return charmap.Windows874
	case 932:
		return japanese.ShiftJIS
	case 936:
		return simplifiedchinese.GBK
	case 949:
		return korean.EUCKR
	case 950:
		return traditionalchinese.Big5
	case 10000:
		return charmap.Macintosh
	case 1250:
		return charmap.Windows1250
	case 1251:
		return charmap.Windows1251
	case 1253:
		return charmap.Windows1253
	case 1254:
		return charmap.Windows1254
	case 1255:
		return charmap.Windows1255
	case 1256:
		return charmap.Windows1256
	case 1257:
		return charmap.Windows1257
	case 1258:
		return charmap.Windows1258
	case 65001:
		return unicode.UTF8
	default:
		return charmap.Windows1252
	}
}
