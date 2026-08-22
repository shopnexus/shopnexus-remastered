// Package lang picks which language a message is written in, and supplies the template
// helpers that depend on it.
//
// It exists because two senders now render copy for the same reader: the transactional
// mail in provider/notify/smtp, and the notification feed's own copybook. Both have to
// resolve `vi-VN` to the same file and print 250000 VND the same way, and the second one
// started life as a copy of the first's private helpers.
package lang

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

// The languages this marketplace writes in. Anything else falls back to English rather
// than sending nothing.
const (
	VI       = "vi"
	EN       = "en"
	Fallback = EN
)

// All is every language copy exists in, and the list a loader walks to demand a file per
// language at startup.
var All = [...]string{VI, EN}

// Of picks the copy for a BCP 47 locale: the base language decides, and anything unknown
// falls back to English.
func Of(locale string) string {
	if strings.HasPrefix(locale, VI) {
		return VI
	}
	return Fallback
}

// Funcs are the template helpers, bound to the language of the set they serve — which is
// what lets `money` group digits the way that language does without every template having
// to say so.
//
// text/template.FuncMap, which html/template aliases, so one map serves both.
func Funcs(l string) template.FuncMap {
	return template.FuncMap{
		"money": func(amount, currency any) string { return Money(l, amount, currency) },
	}
}

// Money is the same formatter the `money` template helper applies, callable from Go.
//
// Exported because one caller now needs an amount outside a template: the mail frame puts
// the escrowed sum in a box the frame owns, and which mails carry a sum at all is a fact
// about the kind rather than a line of copy.
func Money(l string, amount, currency any) string {
	sep := ","
	if l == VI {
		sep = "."
	}
	return money(amount, currency, sep)
}

// money renders an amount the way this platform stores it: unscaled, because VND has no
// minor unit and every rail here settles in it. A currency that is not VND is printed with
// its code rather than a symbol this package would have to guess.
func money(amount, currency any, sep string) string {
	n, ok := asInt64(amount)
	if !ok {
		return fmt.Sprint(amount)
	}
	digits := group(n, sep)
	if code, _ := currency.(string); code == "" || code == "VND" {
		return digits + " ₫"
	}
	return digits + " " + fmt.Sprint(currency)
}

// asInt64 accepts the shapes an amount arrives in. Params crosses no wire — the caller is
// in this process — so an int64 is the normal case and the rest are a caller's convenience.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// group inserts a thousands separator. Written out rather than pulled from x/text: this is
// the only place in the codebase that formats an amount for a person, and a language pack
// for one function is a dependency with nothing else to do.
func group(n int64, sep string) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}
