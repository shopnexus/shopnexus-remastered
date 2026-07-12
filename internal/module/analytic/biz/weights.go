package analyticbiz

import (
	"reflect"

	"shopnexus-server/config"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
)

// weightMap reflects PopularityWeights (keyed by yaml tag = Event name) into the
// map[Event]float64 the scoring code needs. Lives here (not on the config type)
// so the central config package stays free of module imports.
func weightMap(w config.PopularityWeights) map[analyticmodel.Event]float64 {
	result := make(map[analyticmodel.Event]float64)

	v := reflect.ValueOf(w)
	t := v.Type()
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		result[analyticmodel.Event(tag)] = v.Field(i).Float()
	}
	return result
}
