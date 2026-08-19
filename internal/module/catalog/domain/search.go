package domain

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"math"
	"strconv"
	"strings"
)

// RawQueryWeight is what the shopper's own words are worth. The server appends them as a probe
// on every search, so base retrieval is this one term and nothing else — there is no second
// implementation of "search without understanding" to keep in step.
//
// Half of a model-written probe, and measured rather than chosen: on this catalogue a raw
// "dien thoai cu" scores P@10 0.00 on its own and 0.50 through the model's rewrite, and adding
// the raw probe back at full weight drags the rewrite down to 0.40. At 0.5 it costs nothing on
// either query tested. That asymmetry is structural — the words reach here misspelled and
// undiacriticked precisely when the understanding stage was needed, so their embedding is the
// weaker evidence by construction. When the model says nothing this is the only probe, and a
// single term's weight cannot change an ordering, so nothing is lost at the bottom either.
const RawQueryWeight = 0.5

// RRFConstant is the `k` of reciprocal rank fusion. 60 is the value the method was published
// with, and it is not a tuning knob here: it sets how fast a rank's contribution decays, and
// every weight below was chosen against it.
const RRFConstant = 60.0

// LegRelevanceFloor is the share of a leg's best score a hit must reach to survive into the
// fusion, applied per leg because that is the only place two scores came out of one operator.
//
// What it actually does, measured over twelve Vietnamese queries on this catalogue: it cuts
// nothing from the dense leg for ten of them. Cosine on bge-m3 sits in a narrow band — the 200th
// hit scores 0.65 to 0.76 of the first — so a relative floor of 0.6 is below the whole band, and
// the dense pool is simply "the nearest 200". It bites only where there is a real cliff:
// "iphone 13", whose one genuine match scores twice its neighbours, is cut from 200 rows to 25.
// On the sparse leg it is the opposite and does the heavy work, keeping a mean of 18 rows of 200,
// because a row sharing no token with the query has an inner product of zero.
//
// Raising it was tried and is worse: 0.8 gives P@10 0.445 and 0.9 gives 0.355 against 0.473 here,
// because a higher floor empties the sparse leg first and that leg is where the precision is. The
// number stays; the claim that it is what keeps unrelated rows off the page does not — what does
// that is the pool now coming only from positive probe legs.
const LegRelevanceFloor = 0.6

// DenseShare and SparseShare split one probe's weight between meaning and words. Equal, and now
// measured rather than guessed: every alternative tried moved P@10 down or not at all — 0.7/0.3
// gives 0.445 and 0.3/0.7 gives 0.455 against 0.473 for an even split. The two legs are not
// symmetric in reach, which is why the split does not need to be: the dense leg contributes
// almost the whole pool while the sparse leg keeps a mean of 18 rows, nearly all of them already
// in the dense set. Sparse therefore acts as a precision bonus on rows both legs agree about, and
// an even split is what sizes that bonus at roughly forty dense ranks.
const (
	DenseShare  = 0.5
	SparseShare = 0.5
)

// AttrWeight is what each kind of signal is worth. The model never writes a number: it says
// which attributes matter and in what order, and these decide how much that moves a page. That
// is what keeps relevance reproducible — two identical requests rank identically, and a prompt
// change can be compared against the one before it.
//
// Starting values, to be measured. Nothing has calibrated them against real queries yet; the
// golden query set in the design's Deferred section is what would.
// AttrWeight is what each kind of signal is worth. The model never writes a number: it says
// which attributes matter and in what order, and these decide how much that moves a page. That is
// what keeps relevance reproducible — two identical requests rank identically, and a prompt change
// can be compared against the one before it.
//
// Category is the one measured on this catalogue, over a query whose pool is genuinely mixed
// ("điện thoại cũ", where dense similarity alone puts a phone pouch above a phone) and one whose
// pool is not ("áo thun nam", already all inside its category). P@10 against a wrong category
// guess and a right one:
//
//	weight   wrong guess   right guess
//	  0.15      0.50          0.50        neither helps nor harms
//	  0.30      0.50          0.60
//	  0.60      0.50          0.70        the most a right guess buys before a wrong one costs
//	  1.00      0.20          0.80        a wrong guess now rewrites the page
//
// So 0.6 is the ceiling of the free range, not a feel. The reason a weight this large is safe is
// that a boost lifts a whole group at once and leaves the order inside it alone: it can move
// category members past non-members, which is the point, but it cannot reorder the members
// themselves. Arithmetic about what one row's score can jump — a match is worth more than the
// entire span of dense ranks — reads as alarming and predicts the wrong thing; it was tried, the
// weights were cut to a tenth of these, and the mixed pool filled with bags and headphones again.
//
// The other three keep the scale category was set on, and are *not* individually measured. Setting
// them from reasoning alone is the mistake above, so they are left where they were until a query
// set exists that separates them.
var AttrWeight = map[string]float64{
	AttrProbes:    1.0,
	AttrCategory:  0.6,
	AttrTag:       0.5,
	AttrPrice:     0.4,
	AttrCondition: 0.3,
}

// The attributes a signal may name. A model answer outside this set is dropped: the set is also
// the JSON Schema's enum, so it is stated twice on purpose — a schema is what we ask for, this
// is what we accept.
//
// There is no place attribute. Everything here is resolved against real rows, and the knowledge
// base a model copies from holds categories, tags and titles — never a province or a ward — so a
// place could only ever be a guess at the codes those columns store, matching nothing and costing
// a scan. The shopper's own location filters are hard predicates and are unaffected; re-adding an
// attribute needs a vocabulary to resolve it against first.
const (
	AttrProbes    = "probes"
	AttrCategory  = "category"
	AttrTag       = "tag"
	AttrPrice     = "price"
	AttrCondition = "condition"
)

// PositionWeight is how much less each later value in a signal's array counts. The array is a
// priority order — the one thing a model is reliably good at — so this is where "prefer A1 over
// A2" becomes a number, on the server's terms. Its length is also the cap on values per signal:
// a fourth value has no weight to be worth anything, so there is one bound and not two.
var PositionWeight = []float64{1.0, 0.5, 0.33}

// The caps on what one statement will carry, the schema's bounds again on the server — a gateway
// that ignored the schema is exactly what the schema cannot enforce. Three boost probes plus the
// raw query the service always appends, and two demotes: six probes, twelve ANN legs.
// MaxPredicates is per sign: four attributes at three values each is the most a well-formed
// answer produces, so it clips nothing that resolved and bounds the six-signal answer that did
// not.
const (
	MaxBoostProbes  = 3
	MaxDemoteProbes = 2
	MaxPredicates   = 12
)

// Signal is one thing the understanding stage decided. Value is always an array, and the array
// is a priority order; its element type follows Attr — a string for every attribute except
// price, which takes {"lt": n} or {"gt": n}.
type Signal struct {
	Attr  string
	Value []jsontext.Value
}

// Understanding is the whole of what the model answered.
type Understanding struct {
	Boosts     []Signal
	Demotes    []Signal
	Understood string
}

// Compiled is Understanding with every name resolved and every weight folded — what the service
// hands to the port as Terms.
type Compiled struct {
	ProbeTexts   []string
	ProbeWeights []float64
	Predicates   []CompiledPredicate
	// Understood is the model's own sentence, carried through so the caller shows the shopper
	// what was searched without holding the raw answer as well.
	Understood string
}

type CompiledPredicate struct {
	Kind   string
	Value  any
	Weight float64
}

// The predicate kinds a compiled signal may name. Equal by construction to port's
// Predicate* constants — domain may not import port, so this is the one duplication the layering
// forces (the same shape api/domain constant pairs already have); catalog's service package
// tests that a rename cannot silently split the two copies, and that neither side grew a kind
// the other does not have.
const (
	PredicateCategory  = "category"
	PredicateTag       = "tag"
	PredicateMinPrice  = "min-price"
	PredicateMaxPrice  = "max-price"
	PredicateCondition = "condition"
)

// Resolver is the catalogue's own vocabulary: the only thing that turns a name a model copied
// back into a row. An id is never asked of a model — it is a token one will happily invent.
type Resolver interface {
	CategoryID(name string) (int64, bool)
	TagSlug(name string) (string, bool)
}

// Compile turns an understanding into weighted terms.
//
// One rule throughout: a value that does not resolve is dropped in silence. A page missing one
// signal is worth serving, and a 500 because a model named a category that does not exist would
// throw away a search the shopper already waited for.
func Compile(u Understanding, resolve Resolver) Compiled {
	out := Compiled{Understood: u.Understood}
	probes := func(signals []Signal, sign float64, cap int) {
		var taken int
		for _, s := range signals {
			if s.Attr != AttrProbes {
				continue
			}
			for i, raw := range s.Value {
				if taken >= cap || i >= len(PositionWeight) {
					break
				}
				text, ok := decodeString(raw)
				if !ok || strings.TrimSpace(text) == "" {
					continue
				}
				out.ProbeTexts = append(out.ProbeTexts, text)
				out.ProbeWeights = append(out.ProbeWeights,
					sign*AttrWeight[AttrProbes]*PositionWeight[i])
				taken++
			}
		}
	}
	probes(u.Boosts, 1, MaxBoostProbes)
	probes(u.Demotes, -1, MaxDemoteProbes)

	predicates := func(signals []Signal, sign float64) {
		var taken int
		for _, s := range signals {
			if taken >= MaxPredicates {
				return
			}
			if s.Attr == AttrProbes {
				continue
			}
			weight, known := AttrWeight[s.Attr]
			if !known {
				continue
			}
			for i, raw := range s.Value {
				if taken >= MaxPredicates || i >= len(PositionWeight) {
					break
				}
				for _, p := range compilePredicate(s.Attr, raw, resolve) {
					if taken >= MaxPredicates {
						break
					}
					// Position decays a *preference* — the second category the model named is a
					// weaker guess than the first. A price is not that: "300k to 500k" arrives as
					// two array entries which are two halves of one constraint, so decaying the
					// second one silently weakens whichever end the model happened to write last.
					// It cost a range query every in-range result on this catalogue.
					position := PositionWeight[i]
					if s.Attr == AttrPrice {
						position = 1
					}
					p.Weight = sign * weight * position
					out.Predicates = append(out.Predicates, p)
					taken++
				}
			}
		}
	}
	predicates(u.Boosts, 1)
	predicates(u.Demotes, -1)
	return out
}

// compilePredicate resolves one value. It answers a slice because one price object can carry
// both a floor and a ceiling; every other attribute answers zero or one.
func compilePredicate(attr string, raw jsontext.Value, resolve Resolver) []CompiledPredicate {
	switch attr {
	case AttrCategory:
		name, ok := decodeString(raw)
		if !ok {
			return nil
		}
		id, found := resolve.CategoryID(name)
		if !found {
			return nil
		}
		return []CompiledPredicate{{Kind: PredicateCategory, Value: id}}
	case AttrTag:
		name, ok := decodeString(raw)
		if !ok {
			return nil
		}
		slug, found := resolve.TagSlug(name)
		if !found {
			return nil
		}
		return []CompiledPredicate{{Kind: PredicateTag, Value: slug}}
	case AttrCondition:
		value, ok := decodeString(raw)
		if !ok || !validCondition(value) {
			return nil
		}
		return []CompiledPredicate{{Kind: PredicateCondition, Value: value}}
	case AttrPrice:
		var bound struct {
			Lt *int64 `json:"lt"`
			Gt *int64 `json:"gt"`
		}
		if err := json.Unmarshal(raw, &bound); err != nil {
			return nil
		}
		var out []CompiledPredicate
		if bound.Lt != nil && *bound.Lt > 0 {
			out = append(out, CompiledPredicate{Kind: PredicateMaxPrice, Value: *bound.Lt})
		}
		if bound.Gt != nil && *bound.Gt > 0 {
			out = append(out, CompiledPredicate{Kind: PredicateMinPrice, Value: *bound.Gt})
		}
		return out
	}
	return nil
}

func decodeString(raw jsontext.Value) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func validCondition(v string) bool {
	return Condition(v) == ConditionNew || Condition(v) == ConditionUsed || Condition(v) == ConditionDamaged
}

// SelectivityRef is the ln(N/n) at which a predicate keeps the whole weight AttrWeight gave it.
// At 1.0 that is n/N = 1/e, so nothing is scaled until a signal matches more than about a third of
// the catalogue, and then only in proportion. ln rather than the share itself because the
// interesting range is the common end, where a signal stops discriminating at all.
//
// It was shipped at 3.0 — full weight below 5% of the catalogue — and measured down to 1.0. What
// 3.0 got wrong is the thing IDF cannot see: a predicate here is often not evidence *about*
// relevance but a constraint the shopper *stated*. Somebody typing "dt cu" said `used`, and
// damping it because 30% of a second-hand marketplace is used cost that query P@5 0.40 -> 0.20.
// At 1.0 `used` (ln 1.21) is left alone entirely, while `new` — 65.6% of this catalogue, which is
// barely a signal whoever typed it — still falls to a fifth of its weight, and that is the case
// the whole mechanism was built for: before it, a hair comb and a travel SIM outranked actual
// phones on "ip mới" purely for being new (nDCG@10 0.146 -> 0.327 with the damping in place).
//
// Both 1.0 and 4.5 beat 3.0 on the nine data points available, which is how you know that surface
// is noise and not a gradient. So this is chosen for what it means rather than for what it scores:
// damp only what is nearly no information at all. Settling it properly needs the labelled query
// set the design's Deferred section names.
const SelectivityRef = 1.0

// Selectivity is how common each thing a predicate can name is, over the whole active catalogue.
// Total is the active-listing count; Counts holds one entry per (kind, key), spelled the way
// SelectivityKeyOf spells it. A key with no entry is a signal nothing was counted for, and
// ScaleBySelectivity then leaves its weight exactly as configured — guessing at a count is worse
// than not scaling, because a wrong guess moves a page in a direction nobody can trace.
type Selectivity struct {
	Total  int64
	Counts map[SelectivityKey]int64
}

type SelectivityKey struct {
	Kind string
	Key  string
}

// SelectivityKeyOf is the countable half of the predicate set: a category id, a tag slug, a
// condition label. Price is absent on purpose — its bounds are arbitrary numbers a model wrote,
// so there is nothing to have counted ahead of time and it keeps its static weight.
func SelectivityKeyOf(p CompiledPredicate) (SelectivityKey, bool) {
	switch p.Kind {
	case PredicateCategory:
		id, ok := p.Value.(int64)
		if !ok {
			return SelectivityKey{}, false
		}
		return SelectivityKey{Kind: p.Kind, Key: strconv.FormatInt(id, 10)}, true
	case PredicateTag, PredicateCondition:
		value, ok := p.Value.(string)
		if !ok {
			return SelectivityKey{}, false
		}
		return SelectivityKey{Kind: p.Kind, Key: value}, true
	}
	return SelectivityKey{}, false
}

// ScaleBySelectivity weighs each predicate by how rare the thing it names actually is, because
// AttrWeight can only say what an *attribute* is worth and two values of one attribute are not
// equally informative: on this catalogue `condition=new` matches two thirds of the marketplace and
// `condition=damaged` a twentieth, and a fixed per-attribute weight moves a page by the same
// amount for both.
//
// The sign is untouched, so a demotion of something common is damped exactly as a boost of it is.
func ScaleBySelectivity(ps []CompiledPredicate, sel Selectivity) []CompiledPredicate {
	if sel.Total <= 0 {
		return ps
	}
	out := make([]CompiledPredicate, len(ps))
	copy(out, ps)
	for i := range out {
		key, countable := SelectivityKeyOf(out[i])
		if !countable {
			continue
		}
		n := sel.Counts[key]
		if n <= 0 {
			continue
		}
		out[i].Weight *= selectivityFactor(sel.Total, n)
	}
	return out
}

// selectivityFactor is clamped at both ends. Above 1 would push a weight past the value it was
// tuned at; below 0 would flip a boost into a demotion, which a count that has fallen behind a
// deletion (n > Total, the sweep being a pass behind) would otherwise do.
func selectivityFactor(total, n int64) float64 {
	return min(1, max(0, math.Log(float64(total)/float64(n))/SelectivityRef))
}
