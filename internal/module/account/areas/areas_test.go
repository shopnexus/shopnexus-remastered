package areas_test

import (
	"testing"

	"shopnexus/internal/module/account/areas"
)

// The codes are the contract: a client sends back what it was given, and what it was given has to be
// the zero-padded string the column stores — an unpadded "1" is a province nothing matches.
func TestChildren_CodesAreThePaddedFormTheColumnsStore(t *testing.T) {
	provinces, ok, err := areas.Children("")
	if err != nil || !ok {
		t.Fatalf("Children(\"\") = ok %v, err %v", ok, err)
	}
	if len(provinces) != 63 {
		t.Fatalf("provinces = %d, want the whole country", len(provinces))
	}
	byCode := map[string]areas.Area{}
	for _, p := range provinces {
		if p.Kind != areas.KindProvince {
			t.Fatalf("province = %+v, want it kinded as one", p)
		}
		if len(p.Code) != 2 {
			t.Fatalf("province code = %q, want two digits", p.Code)
		}
		byCode[p.Code] = p
	}
	// The codes the seed and the order snapshots already use.
	for _, code := range []string{"01", "31", "48", "79", "92"} {
		if _, ok := byCode[code]; !ok {
			t.Errorf("province %q is missing", code)
		}
	}

	wards, ok, err := areas.Children("79")
	if err != nil || !ok {
		t.Fatalf("Children(\"79\") = ok %v, err %v", ok, err)
	}
	if len(wards) == 0 {
		t.Fatal("Ho Chi Minh City has no wards")
	}
	for _, w := range wards {
		if w.Kind != areas.KindWard || len(w.Code) != 5 {
			t.Fatalf("ward = %+v, want a five-digit ward", w)
		}
	}
	// Two tiers: a ward is under its province directly, so the district level of the source is gone
	// rather than reachable by accident.
	if _, ok, _ := areas.Children("760"); ok {
		t.Error("a district code still answers, so the import kept a level addresses do not have")
	}
}

// A code that names nothing is not found, never an empty list: an empty list tells a client the code
// was real and simply has nothing under it, which is how a typo becomes a silently empty select.
func TestChildren_UnknownCodeIsNotFound(t *testing.T) {
	for _, code := range []string{"99", "00", "abc", "0079"} {
		if _, ok, err := areas.Children(code); ok || err != nil {
			t.Errorf("Children(%q) = ok %v, err %v, want not found", code, ok, err)
		}
	}
}
