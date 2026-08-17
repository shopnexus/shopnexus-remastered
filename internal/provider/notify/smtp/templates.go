package smtp

import (
	"fmt"
	"html/template"
	"io/fs"
	"strconv"
	"strings"

	"shopnexus/internal/provider/notify"
)

// The transactional mail this API sends, loaded from the templates/mail directory at the
// repository root (see package templates for why the files live there).
//
// One file per kind per language, each defining "subject", "title", "lead" and "action",
// optionally overriding "footer" and "extra". The frame — the layout, the button, the
// fallback link, the automated-mail notice — is a file of its own per language, so adding
// a mail is writing copy and never copying markup.

// mailKinds is every kind this package has copy for.
//
// A list in code rather than a walk of the directory, in both directions: a template file
// nobody named here is dead weight nothing sends, and a Kind named here with no file is a
// mail that would fail at 3am — so loadMails refuses to start instead.
var mailKinds = []notify.Kind{
	notify.KindEmailVerification,
	notify.KindPasswordReset,
	notify.KindOrderPlaced,
	notify.KindOrderReceived,
	notify.KindOrderCompleted,
	notify.KindOrderCancelled,
	notify.KindRefundResolved,
	notify.KindOrderUnconfirmed,
}

// The languages this marketplace writes in. Anything else falls back to English rather
// than sending nothing.
const (
	langVI       = "vi"
	langEN       = "en"
	langFallback = langEN
)

var languages = [...]string{langVI, langEN}

// blocks every mail file has to define. The frame supplies "footer" and "extra", so those
// are overrides rather than requirements.
var requiredBlocks = [...]string{"subject", "title", "lead", "action"}

// mailData is what a template is executed against. Params is reached as `.Params.order_id`,
// and a key the caller did not send is a render failure — see missingkey below.
type mailData struct {
	Lang   string
	Link   string
	Params map[string]any
}

// mail is one kind in one language: the parsed set, plus the name of the frame to execute
// for the body. The subject is a named block in the same set.
type mail struct {
	set   *template.Template
	frame string
}

// loadMails parses every kind × language at startup, so a missing file, an unparseable
// template or a mail that forgot to define its subject is a process that does not come up
// — rather than a send that fails on the one night it matters.
func loadMails(fsys fs.FS) (map[notify.Kind]map[string]*mail, error) {
	out := make(map[notify.Kind]map[string]*mail, len(mailKinds))
	for _, kind := range mailKinds {
		byLang := make(map[string]*mail, len(languages))
		for _, lang := range languages {
			m, err := loadMail(fsys, kind, lang)
			if err != nil {
				return nil, err
			}
			byLang[lang] = m
		}
		out[kind] = byLang
	}
	return out, nil
}

func loadMail(fsys fs.FS, kind notify.Kind, lang string) (*mail, error) {
	frame := "frame." + lang + ".html"
	file := string(kind) + "." + lang + ".html"

	// Two Parse calls rather than one with both files: redefinition across successive
	// parses is defined behaviour, and it is what lets a mail override the frame's default
	// "footer". Parsing them together leaves which definition wins up to argument order.
	set, err := template.New(frame).Funcs(funcs(lang)).Option("missingkey=error").ParseFS(fsys, frame)
	if err != nil {
		return nil, fmt.Errorf("parse mail frame %s: %w", frame, err)
	}
	if set, err = set.ParseFS(fsys, file); err != nil {
		return nil, fmt.Errorf("parse mail template %s: %w", file, err)
	}
	for _, block := range requiredBlocks {
		if set.Lookup(block) == nil {
			return nil, fmt.Errorf("mail template %s does not define %q", file, block)
		}
	}
	return &mail{set: set, frame: frame}, nil
}

// funcs are the template helpers, bound to the language of the set they serve — which is
// what lets `money` group digits the way that language does without every template
// having to say so.
func funcs(lang string) template.FuncMap {
	sep := ","
	if lang == langVI {
		sep = "."
	}
	return template.FuncMap{
		"money": func(amount, currency any) string { return formatMoney(amount, currency, sep) },
	}
}

// formatMoney renders an amount the way this platform stores it: unscaled, because VND has
// no minor unit and every rail here settles in it. A currency that is not VND is printed
// with its code rather than a symbol this package would have to guess.
func formatMoney(amount, currency any, sep string) string {
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

// language picks the copy for a BCP 47 locale: the base language decides, and anything
// unknown falls back to English.
func language(locale string) string {
	if strings.HasPrefix(locale, langVI) {
		return langVI
	}
	return langFallback
}
