// Package areas is Vietnam's administrative divisions, the vocabulary every address in this
// platform is written in: `contact.province_code`/`district_code`/`ward_code`, the snapshot an
// order freezes, and the area filters on the listing browse all name codes from this list.
//
// Vendored rather than fetched: both clients were solving it themselves, one by calling
// provinces.open-api.vn from the browser on every page that shows an address form — a third party
// between a seller and their own address — and one by hardcoding thirteen of the sixty-three
// provinces. The list changes when the National Assembly says so, which is not often and never
// while a request is in flight, so it is a file in the binary and not a table, a cache or a
// dependency that can be down. Refresh it by re-importing the source, not by editing it.
//
// Two tiers, because that is what an address here has: province then ward. The source still carries
// the district level and the import drops it, which is also why a province answers every one of its
// wards at once — up to 549, so a client searches rather than scrolls.
//
// Source: provinces.open-api.vn (GSO codes), imported 2026-08-05. Codes are the zero-padded strings
// the columns store — two digits for a province, five for a ward — because a client sends back
// exactly what it was given.
package areas

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

// The two levels, as the `kind` a client renders from.
const (
	KindProvince = "province"
	KindWard     = "ward"
)

//go:embed vn.json
var files embed.FS

// Area is one division: what to send back as a code, and what to show.
type Area struct {
	Code string
	Name string
	Kind string
}

type ward struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type province struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Wards []ward `json:"wards"`
}

var (
	once      sync.Once
	loadErr   error
	provinces []Area
	// wards is keyed by province code: the one level there is below a province.
	wards map[string][]Area
)

// Children answers the divisions one level under parent: the provinces when parent is empty, a
// province's wards otherwise. The second result is false for a code that names nothing, which is a
// 404 rather than an empty list — an empty list would tell a client the code was real.
func Children(parent string) ([]Area, bool, error) {
	if err := load(); err != nil {
		return nil, false, err
	}
	if parent == "" {
		return provinces, true, nil
	}
	found, ok := wards[parent]
	return found, ok, nil
}

// load parses the embedded file once. A failure here is a broken build rather than a runtime
// condition, but it is still returned: panicking in a package variable would take the whole gateway
// down for a route nobody may have called.
func load() error {
	once.Do(func() {
		raw, err := files.ReadFile("vn.json")
		if err != nil {
			loadErr = fmt.Errorf("read administrative areas: %w", err)
			return
		}
		var parsed []province
		if err := json.Unmarshal(raw, &parsed); err != nil {
			loadErr = fmt.Errorf("parse administrative areas: %w", err)
			return
		}
		provinces = make([]Area, 0, len(parsed))
		wards = make(map[string][]Area, len(parsed))
		for _, p := range parsed {
			provinces = append(provinces, Area{Code: p.Code, Name: p.Name, Kind: KindProvince})
			out := make([]Area, 0, len(p.Wards))
			for _, w := range p.Wards {
				out = append(out, Area{Code: w.Code, Name: w.Name, Kind: KindWard})
			}
			wards[p.Code] = out
		}
	})
	return loadErr
}
