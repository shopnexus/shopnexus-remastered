package catalog

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
	"shopnexus/internal/provider/llm"
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
	// Relevance whatever the caller asked for: this draw is vocabulary, not the answer. Under
	// sort=price-asc the ten cheapest rows of the pool would be what the model is shown as the
	// marketplace's words, so the brand and model words the knowledge base exists to supply go
	// missing exactly when the shopper narrowed.
	draw.Sort = port.SortRelevance
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

// searchTerms runs the three stages and answers the terms the retrieval ranks by, plus what to
// tell the shopper.
//
// The raw query goes in last and always. Two things follow: base retrieval is not a separate code
// path — it is this function with a model that said nothing — and a model that misreads a query
// narrows the ranking instead of replacing it.
func (s *Service) searchTerms(ctx context.Context, req catalogapi.ListListingsRequest, filter port.ListingFilter) ([]port.Term, searchAnswer, error) {
	probe, err := s.queryProbe(ctx, req.Query)
	if err != nil {
		return nil, searchAnswer{}, err
	}
	kb := s.knowledge(ctx, probe, filter)
	compiled := domain.Compile(s.understand(ctx, req.Query, kb), kb)

	texts := compiled.ProbeTexts
	weights := compiled.ProbeWeights
	// The shopper's own words, unless the model already named them as a boost. Only a boost
	// counts: a model that put the query itself in `demotes` would otherwise leave the statement
	// with nothing but a negative probe of it, and `ORDER BY score DESC` then ranks the catalogue
	// by how unlike the query it is. This is what makes a malformed answer degrade to the base
	// search rather than invert it.
	if !containsFold(searched(texts, weights), req.Query) {
		texts = append(texts, req.Query)
		weights = append(weights, domain.RawQueryWeight)
	}
	probes, err := s.queryProbes(ctx, texts)
	if err != nil {
		return nil, searchAnswer{}, err
	}

	terms := make([]port.Term, 0, len(probes)+len(compiled.Predicates))
	for i := range probes {
		terms = append(terms, port.Term{Weight: weights[i], Probe: &probes[i]})
	}
	for _, p := range compiled.Predicates {
		terms = append(terms, port.Term{
			Weight:    p.Weight,
			Predicate: &port.Predicate{Kind: p.Kind, Value: p.Value},
		})
	}
	return terms, searchAnswer{understood: compiled.Understood, probes: searched(texts, weights)}, nil
}

// searched is the phrases the ranking was pulled *towards*: the positive-weight probes. It is
// what the shopper is shown and what the raw query is deduped against, which is one question
// asked twice — a demoted phrase is the opposite of what was searched for.
// Listing one under "searching for" tells them the feed is looking for the very thing it was told
// to avoid; the first live query surfaced that, "ao thu unilo" answering with "ô tô" among its
// probes.
func searched(texts []string, weights []float64) []string {
	out := make([]string, 0, len(texts))
	for i, text := range texts {
		if weights[i] > 0 {
			out = append(out, text)
		}
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if normalizeName(s) == normalizeName(want) {
			return true
		}
	}
	return false
}

// understand asks the model what the query meant. One function, deliberately: it is the seam a
// structured-query cache wraps, and the design leaves that cache for later.
//
// An answer that cannot be read is an empty Understanding, not an error. The caller appends the
// shopper's own words as a probe regardless, so nothing usable from here means the search is the
// base retrieval it would have been anyway.
func (s *Service) understand(ctx context.Context, query string, kb knowledge) domain.Understanding {
	answer, err := s.llm.Complete(ctx, llm.CompleteParams{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: understandingPrompt(kb)},
			{Role: llm.RoleUser, Content: query},
		},
		ResponseFormat: &llm.ResponseFormat{Name: "search_understanding", Schema: understandingSchema},
		// This is classification against a list, not writing.
		Temperature: new(0.0),
	})
	if err != nil {
		s.log.Warn("understand search query", "err", err)
		return domain.Understanding{}
	}
	var wire struct {
		Boosts  []wireSignal `json:"boosts"`
		Demotes []wireSignal `json:"demotes"`
		Meaning string       `json:"understood"`
	}
	if err := json.Unmarshal([]byte(answer.Message.Content), &wire); err != nil {
		s.log.Warn("decode search understanding", "err", err)
		return domain.Understanding{}
	}
	return domain.Understanding{
		Boosts:     signalsOf(wire.Boosts),
		Demotes:    signalsOf(wire.Demotes),
		Understood: wire.Meaning,
	}
}

// wireSignal keeps each value as raw JSON: the element type follows attr — a string everywhere
// except price, which is an object — and domain.Compile is what decides which.
type wireSignal struct {
	Attr  string           `json:"attr"`
	Value []jsontext.Value `json:"value"`
}

func signalsOf(in []wireSignal) []domain.Signal {
	out := make([]domain.Signal, 0, len(in))
	for _, s := range in {
		out = append(out, domain.Signal{Attr: s.Attr, Value: s.Value})
	}
	return out
}

// understandingSchema is what the model must answer. One item shape for both lists, and `value`
// is always an array — the array is a priority order. Always-an-array is what keeps this a
// `oneOf` over element types instead of a discriminated union over `value`, which models get
// wrong far more often.
var understandingSchema = jsontext.Value(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["boosts", "demotes", "understood"],
  "properties": {
    "boosts":  {"type": "array", "maxItems": 6, "items": {"$ref": "#/$defs/signal"}},
    "demotes": {"type": "array", "maxItems": 4, "items": {"$ref": "#/$defs/signal"}},
    "understood": {"type": "string", "maxLength": 200}
  },
  "$defs": {
    "signal": {
      "type": "object",
      "additionalProperties": false,
      "required": ["attr", "value"],
      "properties": {
        "attr": {"type": "string", "enum": ["probes", "category", "tag", "price", "condition"]},
        "value": {
          "type": "array",
          "maxItems": 3,
          "items": {
            "oneOf": [
              {"type": "string", "maxLength": 200},
              {
                "type": "object",
                "additionalProperties": false,
                "properties": {"lt": {"type": "integer"}, "gt": {"type": "integer"}}
              }
            ]
          }
        }
      }
    }
  }
}`)

// understandingPrompt inlines the marketplace's vocabulary, because the model must choose from
// *this* catalogue: a category or tag it invents resolves to nothing and is dropped, which costs
// the shopper a signal. Titles are there for the words no tag carries — a brand, a model number.
func understandingPrompt(kb knowledge) string {
	var b strings.Builder
	b.WriteString(`You read a search box on a Vietnamese second-hand marketplace and work out what the shopper meant.

They type quickly: missing diacritics ("ao thun" for "áo thun"), misspellings ("unilo" for "uniqlo"),
and vague asks ("quà tặng sinh nhật rẻ"). Your job is to turn that into search signals.

Answer boosts and demotes. Each is {"attr": ..., "value": [...]} and the array is a priority order,
strongest first.

- "probes": search phrases in correct Vietnamese. This is where you fix spelling and diacritics.
  Put the most likely reading first. Demote phrases for things the shopper clearly does not want.
- "category", "tag": copy a name EXACTLY from the lists below. Never invent one.
- "price": {"lt": n} or {"gt": n} in Vietnamese dong, only when they said something about price.
- "condition": one of new, used, damaged.

Leave out any attribute you are not confident about. A missing signal costs a little precision;
a wrong one sends the shopper the wrong goods.

"understood" is one short Vietnamese phrase describing what you took the query to mean. It is shown
to the shopper, so write it for them.

Categories:
`)
	for _, c := range kb.Categories {
		b.WriteString("- " + c.Name + "\n")
	}
	if len(kb.Tags) > 0 {
		b.WriteString("\nTags near this query:\n")
		for _, t := range kb.Tags {
			b.WriteString("- " + t + "\n")
		}
	}
	if len(kb.Titles) > 0 {
		b.WriteString("\nListings currently near this query, for the words they use:\n")
		for _, t := range kb.Titles {
			b.WriteString("- " + t + "\n")
		}
	}
	return b.String()
}
