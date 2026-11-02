package report

import (
	"strings"

	"golang.org/x/net/html"
)

// plainText converts an HTML metadata fragment to whitespace-normalized text.
// Tags become spaces so inline markup cannot join words; script, style and
// head contents are dropped.
func plainText(s string) string {
	if s == "" {
		return ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skipDepth := 0
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		switch tokenType {
		case html.TextToken:
			if skipDepth == 0 {
				b.Write(tokenizer.Text())
			}
		case html.StartTagToken:
			name, _ := tokenizer.TagName()
			if isSkippedHTMLTag(name) {
				skipDepth++
			}
			b.WriteByte(' ')
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			if isSkippedHTMLTag(name) && skipDepth > 0 {
				skipDepth--
			}
			b.WriteByte(' ')
		case html.SelfClosingTagToken:
			b.WriteByte(' ')
		}
	}
	return normalizeMeta(b.String())
}

func isSkippedHTMLTag(name []byte) bool {
	switch string(name) {
	case "head", "script", "style":
		return true
	default:
		return false
	}
}
