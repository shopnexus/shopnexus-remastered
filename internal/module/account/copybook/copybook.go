// Package copybook renders a notification's words.
//
// It is the feed's counterpart to templates/mail: the row stores a kind and the facts, and the
// sentence is written here, in the language of whoever is reading it. Its own package because
// domain may not import shared/lang, and the account service needs both.
package copybook

import (
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/lang"
)

// entry is one kind's copy as it is written in the file.
type entry struct {
	Title string `yaml:"title"`
	Body  string `yaml:"body"`
}

// rendered is the same pair parsed. Both are templates: a title says which order, and a body
// says how much.
type rendered struct {
	title *template.Template
	body  *template.Template
}

// Book is every kind in every language, parsed once.
type Book struct {
	byLang map[string]map[domain.Kind]rendered
}

// Load parses one file per language and demands copy for every kind the domain knows, so a kind
// added without words is a process that does not come up rather than a blank row in somebody's
// feed. Same bargain as the mail templates, for the same reason.
func Load(fsys fs.FS) (*Book, error) {
	b := &Book{byLang: make(map[string]map[domain.Kind]rendered, len(lang.All))}
	for _, l := range lang.All {
		file := l + ".yaml"
		raw, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("read notification copy %s: %w", file, err)
		}
		var entries map[domain.Kind]entry
		if err := yaml.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("parse notification copy %s: %w", file, err)
		}
		set := make(map[domain.Kind]rendered, len(domain.Kinds))
		for _, kind := range domain.Kinds {
			e, ok := entries[kind]
			if !ok || e.Title == "" {
				return nil, fmt.Errorf("notification copy %s has no title for kind %q", file, kind)
			}
			r, err := parse(l, kind, e)
			if err != nil {
				return nil, err
			}
			set[kind] = r
		}
		// A kind in the file that the domain does not know is dead copy, and more often a typo
		// in the key than a deliberate spare — which would otherwise leave the real kind
		// falling back to nothing.
		for kind := range entries {
			if _, ok := domain.SpecOf(kind); !ok {
				return nil, fmt.Errorf("notification copy %s names unknown kind %q", file, kind)
			}
		}
		b.byLang[l] = set
	}
	return b, nil
}

func parse(l string, kind domain.Kind, e entry) (rendered, error) {
	name := string(kind) + "." + l
	// missingkey=error, like the mail: a payload key the emitter forgot has to fail here rather
	// than render "<no value>" into somebody's feed.
	title, err := template.New(name + ".title").Funcs(lang.Funcs(l)).Option("missingkey=error").Parse(e.Title)
	if err != nil {
		return rendered{}, fmt.Errorf("parse notification title %s: %w", name, err)
	}
	if e.Body == "" {
		return rendered{title: title}, nil
	}
	body, err := template.New(name + ".body").Funcs(lang.Funcs(l)).Option("missingkey=error").Parse(e.Body)
	if err != nil {
		return rendered{}, fmt.Errorf("parse notification body %s: %w", name, err)
	}
	return rendered{title: title, body: body}, nil
}

// Render writes one notification's title and supporting line in the reader's language.
//
// It never fails the read: a template that cannot render — a payload key an emitter forgot —
// answers what it has and leaves the rest empty, because a feed that 500s over one row's
// missing total is worse than a row with no subtitle. Load already refused the mistakes that
// can be caught without a payload.
func (b *Book) Render(locale string, kind domain.Kind, payload map[string]any) (title, body string) {
	r, ok := b.byLang[lang.Of(locale)][kind]
	if !ok {
		return "", ""
	}
	if payload == nil {
		// An empty map, not nil: missingkey=error reports a missing key, but a nil map is a
		// nil-pointer evaluation the templates would each have to guard against.
		payload = map[string]any{}
	}
	return exec(r.title, payload), exec(r.body, payload)
}

func exec(t *template.Template, payload map[string]any) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	if err := t.Execute(&sb, payload); err != nil {
		return ""
	}
	return sb.String()
}
