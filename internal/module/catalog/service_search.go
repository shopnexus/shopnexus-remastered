package catalog

import (
	"context"
	"strings"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// How much vocabulary the model is shown. The category tree goes in whole — it is small, static,
// and the only way a named category exists. Tags and titles are the nearest few: enough to carry
// the brand and model words no tag holds, few enough that the prompt stays about the query.
const (
	knowledgeTags   = 20
	knowledgeTitles = 10
)

// knowledge is what the marketplace knows, as names a model can copy back. It is also the
// domain.Resolver: the same set that was shown is the set that resolves, so a model cannot be
// blamed for naming something it was never offered.
type knowledge struct {
	Categories []domain.Category
	Tags       []string
	Titles     []string
}

// knowledge assembles it from one embedding of the raw query — three index-served reads and no
// model call. Every read is best-effort: a category list, a tag ranking or a title draw that
// fails leaves that part of the vocabulary empty rather than failing the search, because a model
// with less vocabulary still answers usefully and the raw-query probe underneath the whole
// pipeline does not depend on any of it.
//
// The titles come from a retrieval on the raw query, which is the one place the shopper's own
// spelling is used: they are vocabulary, not an answer, and a garbled query still lands near the
// words it garbled. What the model must not be given is the *result* of that retrieval as if it
// were the answer — that is how a garbled query's noise gets carried forward.
func (s *Service) knowledge(ctx context.Context, probe port.Probe, filter port.ListingFilter) knowledge {
	var out knowledge
	categories, err := s.repo.ListCategories(ctx)
	if err != nil {
		s.log.Warn("read categories for search knowledge", "err", err)
	}
	out.Categories = categories

	tags, err := s.repo.NearestTags(ctx, []port.Vector{probe.Dense}, nil, 0, knowledgeTags)
	if err != nil {
		s.log.Warn("read nearest tags for search knowledge", "err", err)
	}
	for _, t := range tags {
		out.Tags = append(out.Tags, t.Tag.Slug)
	}

	draw := filter
	draw.Terms = []port.Term{{Weight: domain.RawQueryWeight, Probe: &probe}}
	draw.Offset, draw.Limit = 0, knowledgeTitles
	rows, _, err := s.repo.ListListings(ctx, draw)
	if err != nil {
		s.log.Warn("read nearest titles for search knowledge", "err", err)
	}
	for _, row := range rows {
		out.Titles = append(out.Titles, row.Name)
	}
	return out
}

// CategoryID is half of the resolver, delegating to categoryByName — the suggestion route's own
// "line the model copied back" lookup — so there is one spelling of that match, not two.
func (k knowledge) CategoryID(name string) (int64, bool) {
	c, ok := categoryByName(k.Categories, name)
	return c.ID, ok
}

// TagSlug is the resolver's other half. Tags have no second lookup to share with, since a tag
// is already just its slug.
func (k knowledge) TagSlug(name string) (string, bool) {
	want := normalizeName(name)
	for _, slug := range k.Tags {
		if normalizeName(slug) == want {
			return slug, true
		}
	}
	return "", false
}

// normalizeName is what makes a copy match regardless of case or how it collapsed whitespace:
// the only way a model's transcription of a name we showed it can be wrong.
func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
