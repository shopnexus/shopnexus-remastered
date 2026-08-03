package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Letters NFD leaves alone because they are not a base plus a combining mark. The source
// is Spanish, Portuguese and Vietnamese listings, so đ is the one that actually shows up;
// the rest are here so a stray Nordic or German title does not lose a character.
var foldPairs = strings.NewReplacer(
	"đ", "d", "Đ", "d",
	"ø", "o", "Ø", "o",
	"ß", "ss",
	"æ", "ae", "Æ", "ae",
	"œ", "oe", "Œ", "oe",
	"ł", "l", "Ł", "l",
)

// slugify produces the ASCII kebab-case both "listing"."slug" and "tag"."id" are CHECKed
// against: `^[a-z0-9]+(-[a-z0-9]+)*$`. A script that folds to nothing — Thai, Chinese —
// yields "", and the caller drops that rather than inventing a name for it.
func slugify(s string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range norm.NFD.String(foldPairs.Replace(strings.ToLower(s))) {
		switch {
		case unicode.Is(unicode.Mn, r): // the accent NFD split off
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		default:
			pendingDash = true
		}
	}
	return b.String()
}

// clipSlug cuts a slug to n bytes on a dash boundary, so the result is still a slug and not
// a word sawn in half. A cut that already lands on one keeps the last word.
func clipSlug(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut, rest := s[:n], s[n]
	if i := strings.LastIndexByte(cut, '-'); i > 0 && rest != '-' {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

var nonNumeric = regexp.MustCompile(`[^0-9.-]`)

// toInt64 reads a quantity out of whatever the scrape put in the field: a number, a string
// with a thousands separator, a string with a unit. Anything unreadable is 0, because a
// listing with no stock figure is a listing with no stock figure.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case nil:
		return 0
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		f, err := strconv.ParseFloat(nonNumeric.ReplaceAllString(n, ""), 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		return 0
	}
}
