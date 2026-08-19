package domain_test

import (
	"encoding/json/jsontext"
	"math"
	"testing"

	"shopnexus/internal/module/catalog/domain"
)

// fakeResolver stands in for the catalogue's own vocabulary. Only what it knows resolves; the
// rest is dropped, which is the whole contract of a model naming things by name.
type fakeResolver struct{}

func (fakeResolver) CategoryID(name string) (int64, bool) {
	if name == "Áo nam" {
		return 42, true
	}
	return 0, false
}

func (fakeResolver) TagSlug(name string) (string, bool) {
	if name == "uniqlo" {
		return "uniqlo", true
	}
	return "", false
}

func values(raw ...string) []jsontext.Value {
	out := make([]jsontext.Value, 0, len(raw))
	for _, r := range raw {
		out = append(out, jsontext.Value(r))
	}
	return out
}

// A boost's position is its priority, and the multiplier comes from the server's own table —
// never from the model, so two identical requests rank identically.
func TestCompile_PositionDecidesWeight(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts: []domain.Signal{{Attr: "probes", Value: values(`"áo thun nam"`, `"áo phông"`)}},
	}, fakeResolver{})

	if len(got.ProbeTexts) != 2 {
		t.Fatalf("probes = %v, want both", got.ProbeTexts)
	}
	if got.ProbeWeights[0] <= got.ProbeWeights[1] {
		t.Errorf("weights = %v, want the first position to weigh more", got.ProbeWeights)
	}
	// The absolute values are normalized, so what holds is the total rather than any one of them:
	// one attribute's values are worth one attribute however many the model emitted. Asserting
	// w_attr · pos_0 instead is what this test used to do, and it was true only while the score was
	// an unbounded sum over the model's own choice of how many probes to write.
	var total float64
	for _, w := range got.ProbeWeights {
		total += w
	}
	if math.Abs(total-domain.AttrWeight["probes"]*domain.PositionWeight[0]) > 1e-9 {
		t.Errorf("weights sum to %v, want one attribute's worth", total)
	}
}

// A single probe is left exactly as configured: normalizing is for a stack, and scaling one value
// up to fill the attribute would make a model that named one thing louder than one that named three.
func TestCompile_OneProbeIsNotScaledUp(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts: []domain.Signal{{Attr: "probes", Value: values(`"áo thun nam"`)}},
	}, fakeResolver{})

	if len(got.ProbeWeights) != 1 {
		t.Fatalf("weights = %v, want one", got.ProbeWeights)
	}
	if got.ProbeWeights[0] != domain.AttrWeight["probes"]*domain.PositionWeight[0] {
		t.Errorf("weight = %v, want it untouched at w_attr · pos_0", got.ProbeWeights[0])
	}
}

// A demotion is normalized apart from the boosts: sharing one budget would make a demoted phrase
// quietly cost the boosts part of what they are worth.
func TestCompile_DemotesNormalizeApartFromBoosts(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts:  []domain.Signal{{Attr: "probes", Value: values(`"áo khoác nam"`)}},
		Demotes: []domain.Signal{{Attr: "probes", Value: values(`"áo thun"`, `"áo sơ mi"`)}},
	}, fakeResolver{})

	var pos, neg float64
	for _, w := range got.ProbeWeights {
		if w > 0 {
			pos += w
		} else {
			neg -= w
		}
	}
	want := domain.AttrWeight["probes"] * domain.PositionWeight[0]
	if math.Abs(pos-want) > 1e-9 {
		t.Errorf("boosts sum to %v, want %v — a demote must not spend the boost budget", pos, want)
	}
	if math.Abs(neg-want) > 1e-9 {
		t.Errorf("demotes sum to %v, want %v", neg, want)
	}
}

// A demote is the same signal with the sign flipped. There is no second code path for it.
func TestCompile_DemoteIsASign(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Demotes: []domain.Signal{{Attr: "probes", Value: values(`"áo khoác"`)}},
	}, fakeResolver{})

	if len(got.ProbeWeights) != 1 || got.ProbeWeights[0] >= 0 {
		t.Fatalf("weights = %v, want one negative weight", got.ProbeWeights)
	}
}

// Everything is resolved against real rows, and what does not resolve is dropped in silence —
// the rule parseSuggestion already follows. A page with one signal missing is worth serving; an
// error because a model invented a category name is not.
func TestCompile_DropsWhatDoesNotResolve(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts: []domain.Signal{
			{Attr: "category", Value: values(`"Áo nam"`, `"Không có thật"`)},
			{Attr: "condition", Value: values(`"new"`, `"mint"`)},
			{Attr: "nonsense", Value: values(`"x"`)},
		},
	}, fakeResolver{})

	if len(got.Predicates) != 2 {
		t.Fatalf("predicates = %+v, want the category and the condition that exist", got.Predicates)
	}
}

// The caps are the server's, enforced after the schema, because a schema is a request and not a
// guarantee.
func TestCompile_EnforcesTheCaps(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts:  []domain.Signal{{Attr: "probes", Value: values(`"a"`, `"b"`, `"c"`, `"d"`, `"e"`)}},
		Demotes: []domain.Signal{{Attr: "probes", Value: values(`"x"`, `"y"`, `"z"`)}},
	}, fakeResolver{})

	var boosts, demotes int
	for _, w := range got.ProbeWeights {
		if w > 0 {
			boosts++
		} else {
			demotes++
		}
	}
	if boosts != domain.MaxBoostProbes {
		t.Errorf("boost probes = %d, want %d", boosts, domain.MaxBoostProbes)
	}
	if demotes != domain.MaxDemoteProbes {
		t.Errorf("demote probes = %d, want %d", demotes, domain.MaxDemoteProbes)
	}
}

// A price bound is an object rather than a string, and a shape the evaluator cannot read is
// dropped like any other unresolvable value.
func TestCompile_PriceBounds(t *testing.T) {
	got := domain.Compile(domain.Understanding{
		Boosts: []domain.Signal{{Attr: "price", Value: values(`{"lt":50000}`, `{"weird":1}`)}},
	}, fakeResolver{})

	if len(got.Predicates) != 1 {
		t.Fatalf("predicates = %+v, want only the bound that parsed", got.Predicates)
	}
	if got.Predicates[0].Kind != "max-price" || got.Predicates[0].Value != int64(50000) {
		t.Errorf("predicate = %+v, want a max-price of 50000", got.Predicates[0])
	}
}

// Predicates are capped across signals, not just within one. Six signals of three values each is
// a shape the schema allows and the server must not carry: thirty terms is thirty joins over the
// pool for one search.
func TestCompile_CapsPredicatesAcrossSignals(t *testing.T) {
	var boosts []domain.Signal
	for range 6 {
		boosts = append(boosts,
			domain.Signal{Attr: "category", Value: values(`"Áo nam"`, `"Áo nam"`, `"Áo nam"`)})
	}
	got := domain.Compile(domain.Understanding{Boosts: boosts}, fakeResolver{})

	if len(got.Predicates) != domain.MaxPredicates {
		t.Errorf("predicates = %d, want the cap of %d", len(got.Predicates), domain.MaxPredicates)
	}
}

// selectivityOf builds the counts table the service reads, keyed the way the adapter writes it.
func selectivityOf(total int64, counts map[domain.SelectivityKey]int64) domain.Selectivity {
	return domain.Selectivity{Total: total, Counts: counts}
}

// The measurement this exists for: on the live catalogue `condition=new` covers two thirds of the
// marketplace and `condition=damaged` a twentieth, and AttrWeight gives both 0.3.
func TestScaleBySelectivity_RareOutweighsCommon(t *testing.T) {
	const total = 851
	got := domain.ScaleBySelectivity([]domain.CompiledPredicate{
		{Kind: domain.PredicateCondition, Value: "new", Weight: 0.3},
		{Kind: domain.PredicateCondition, Value: "damaged", Weight: 0.3},
	}, selectivityOf(total, map[domain.SelectivityKey]int64{
		{Kind: domain.PredicateCondition, Key: "new"}:     558,
		{Kind: domain.PredicateCondition, Key: "damaged"}: 38,
	}))

	if got[0].Weight >= got[1].Weight {
		t.Errorf("weights = %v, %v, want the rarer condition to weigh more", got[0].Weight, got[1].Weight)
	}
	// ln(851/38) = 3.1, past the reference, so a signal that rare keeps every bit of its
	// configured weight — the scale only ever damps.
	if got[1].Weight != 0.3 {
		t.Errorf("damaged weight = %v, want the configured 0.3 kept", got[1].Weight)
	}
}

// A count nobody has is not an excuse to guess one: the predicate keeps the weight it was
// configured with, which is the ranking that ran before this table existed.
func TestScaleBySelectivity_LeavesWhatItCannotCount(t *testing.T) {
	in := []domain.CompiledPredicate{
		{Kind: domain.PredicateTag, Value: "uniqlo", Weight: 0.5},
		{Kind: domain.PredicateMaxPrice, Value: int64(5_000_000), Weight: 0.4},
	}
	for name, sel := range map[string]domain.Selectivity{
		// A price bound is an arbitrary number a model wrote, so there is nothing to count for
		// it however complete the table is.
		"counted, but price is never scaled": selectivityOf(851, map[domain.SelectivityKey]int64{
			{Kind: domain.PredicateTag, Key: "uniqlo"}: 3,
		}),
		"no counts at all":  selectivityOf(851, nil),
		"no total counted":  selectivityOf(0, map[domain.SelectivityKey]int64{{Kind: domain.PredicateTag, Key: "uniqlo"}: 400}),
		"the key is absent": selectivityOf(851, map[domain.SelectivityKey]int64{{Kind: domain.PredicateTag, Key: "adidas"}: 400}),
	} {
		got := domain.ScaleBySelectivity(in, sel)
		if got[1].Weight != 0.4 {
			t.Errorf("%s: price weight = %v, want the configured 0.4", name, got[1].Weight)
		}
		if name != "counted, but price is never scaled" && got[0].Weight != 0.5 {
			t.Errorf("%s: tag weight = %v, want the configured 0.5", name, got[0].Weight)
		}
	}
}

// A demotion is the same signal with the sign flipped, so damping it must damp its magnitude and
// leave the sign where it was — and a count that has fallen behind a deletion (n past the total)
// must clamp to zero rather than turn a boost into a demotion.
func TestScaleBySelectivity_KeepsTheSign(t *testing.T) {
	got := domain.ScaleBySelectivity([]domain.CompiledPredicate{
		{Kind: domain.PredicateCategory, Value: int64(9360), Weight: -0.6},
		{Kind: domain.PredicateCategory, Value: int64(9359), Weight: 0.6},
	}, selectivityOf(100, map[domain.SelectivityKey]int64{
		{Kind: domain.PredicateCategory, Key: "9360"}: 50,
		{Kind: domain.PredicateCategory, Key: "9359"}: 400,
	}))

	if got[0].Weight >= 0 || got[0].Weight <= -0.6 {
		t.Errorf("demote weight = %v, want it damped and still negative", got[0].Weight)
	}
	if got[1].Weight != 0 {
		t.Errorf("stale-count weight = %v, want 0 — never a sign flip", got[1].Weight)
	}
}

// The input is not written through: the service logs the compiled predicates and a scale that
// mutated them in place would make the two views of one search disagree about which is which.
func TestScaleBySelectivity_DoesNotMutateItsInput(t *testing.T) {
	in := []domain.CompiledPredicate{{Kind: domain.PredicateCondition, Value: "new", Weight: 0.3}}
	domain.ScaleBySelectivity(in, selectivityOf(851, map[domain.SelectivityKey]int64{
		{Kind: domain.PredicateCondition, Key: "new"}: 558,
	}))
	if in[0].Weight != 0.3 {
		t.Errorf("input weight = %v, want it untouched", in[0].Weight)
	}
}
