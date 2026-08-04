package main

import (
	"regexp"
	"strconv"
	"strings"

	catalogdomain "shopnexus/internal/module/catalog/domain"
)

// slugify is catalog's own rule, not a copy of it: `listing.slug` and `tag.id` are CHECKed against
// `^[a-z0-9]+(-[a-z0-9]+)*$`, and the module that owns those columns owns what satisfies them. A
// script that folds to nothing — Thai, Chinese — yields "", and the caller drops that rather than
// inventing a name for it.
func slugify(s string) string { return catalogdomain.SlugifyName(s) }

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
