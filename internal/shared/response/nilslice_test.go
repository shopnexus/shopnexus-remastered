package response

import (
	"encoding/json"
	"testing"
	"time"
)

// A nil json.RawMessage is a nil named []byte. The nil-slice walker must NOT
// rewrite it into an empty []byte — that marshals to "" and encoding/json
// rejects it with "unexpected end of JSON input" (the seller/buyer order list
// and get-order-by-id 500s). It must stay nil so it marshals to "null".
func TestMarshalJSONWithEmptyArrays_NilRawMessageStaysNull(t *testing.T) {
	type doc struct {
		FxSnapshot json.RawMessage `json:"fx_snapshot"` // nullable jsonb -> nil
		Data       json.RawMessage `json:"data"`        // present jsonb
		Tags       []string        `json:"tags"`        // nil slice -> []
		When       time.Time       `json:"when"`        // json.Marshaler, untouched
	}

	got, err := MarshalJSONWithEmptyArrays(doc{
		FxSnapshot: nil,
		Data:       json.RawMessage(`{"a":1}`),
		Tags:       nil,
		When:       time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"fx_snapshot":null,"data":{"a":1},"tags":[],"when":"1970-01-01T00:00:00Z"}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
