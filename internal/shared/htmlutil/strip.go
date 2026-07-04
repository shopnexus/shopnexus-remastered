package htmlutil

import (
	"strings"

	"golang.org/x/net/html"
)

// block-level elements that should produce line breaks.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "tr": true, "blockquote": true, "pre": true,
	"section": true, "article": true, "header": true, "footer": true,
}

// skip elements whose content is not visible text.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
}

// StripHTML extracts plain text from HTML.
// Block elements become newlines, inline tags are removed, entities are decoded,
// and whitespace is collapsed. Suitable for feeding into embeddings.
func StripHTML(s string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(s))

	var b strings.Builder
	skipDepth := 0

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return collapseWhitespace(b.String())

		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)

			if skipTags[tag] {
				skipDepth++
				continue
			}
			if blockTags[tag] {
				b.WriteByte('\n')
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)

			if skipTags[tag] && skipDepth > 0 {
				skipDepth--
				continue
			}
			if blockTags[tag] {
				b.WriteByte('\n')
			}

		case html.TextToken:
			if skipDepth == 0 {
				b.Write(tokenizer.Text())
			}
		case html.CommentToken, html.DoctypeToken:
		}
	}
}

func collapseWhitespace(s string) string {
	// Replace all whitespace runs (except newlines) with single space
	var b strings.Builder
	b.Grow(len(s))

	prevNewline := false
	prevSpace := false

	for _, r := range s {
		switch r {
		case '\n':
			if !prevNewline {
				b.WriteByte('\n')
			}
			prevNewline = true
			prevSpace = false
		case ' ', '\t', '\r', '\u00a0':
			prevSpace = true
			prevNewline = false
		default:
			if prevSpace {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			prevSpace = false
			prevNewline = false
		}
	}

	return strings.TrimSpace(b.String())
}
