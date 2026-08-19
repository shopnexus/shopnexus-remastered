package domain

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
)

// RawQueryWeight is what the shopper's own words are worth. The server appends them as a probe
// on every search, so base retrieval is this one term and nothing else — there is no second
// implementation of "search without understanding" to keep in step.
const RawQueryWeight = 1.0

// RRFConstant is the `k` of reciprocal rank fusion. 60 is the value the method was published
// with, and it is not a tuning knob here: it sets how fast a rank's contribution decays, and
// every weight below was chosen against it.
const RRFConstant = 60.0

// LegRelevanceFloor is the share of a leg's best score a hit must reach to survive into the
// fusion. Measured on this catalogue against cosine scores: the genuine hits for a narrow query
// sit at 0.93–1.00 of the best and the first unrelated one at 0.47, while a broad query stays
// above 0.71 all the way down — so 0.6 falls in the gap for both shapes. Applied per leg,
// because that is the only place two scores came out of the same operator.
const LegRelevanceFloor = 0.6

// DenseShare and SparseShare split one probe's weight between meaning and words. Equal to start
// with, and that is a guess: nothing has measured which half this catalogue's queries need more
// of. A missing half contributes nothing rather than handing its share to the other one — less
// evidence is less signal, not the same signal from one side.
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
					p.Weight = sign * weight * PositionWeight[i]
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
