package paginate

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/guregu/null/v6"
)

type SortDir string

const (
	Asc  SortDir = "asc"
	Desc SortDir = "desc"
)

type SortField struct {
	Field string
	Dir   SortDir
}

// ParseSort reads a `?sort=-date_created,score` style param. `-` prefix = desc.
func ParseSort(raw string) []SortField {
	if raw == "" {
		return nil
	}
	var out []SortField
	for tok := range strings.SplitSeq(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		dir := Asc
		switch tok[0] {
		case '-':
			dir, tok = Desc, tok[1:]
		case '+':
			tok = tok[1:]
		}
		out = append(out, SortField{Field: tok, Dir: dir})
	}
	return out
}

// keysetCursor is the opaque position: the sort-tuple values of the last row in
// the sort order (pk tiebreaker last), each as raw JSON so the type survives the
// round-trip when decoded into a typed target — no SQL cast needed.
type keysetCursor struct {
	Keys []json.RawMessage `json:"k"`
}

// EncodeKeyset builds the next cursor from the typed-JSON sort-tuple values of
// the last row, in sort-tuple order. nil => no next page.
func EncodeKeyset(keys []json.RawMessage) null.String {
	if keys == nil {
		return null.String{}
	}
	b, err := json.Marshal(keysetCursor{Keys: keys})
	if err != nil {
		return null.String{}
	}
	return null.StringFrom(base64.StdEncoding.EncodeToString(b))
}

// DecodeKeyset returns the typed-JSON sort-tuple values from a cursor token.
func DecodeKeyset(s string) ([]json.RawMessage, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var c keysetCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return c.Keys, nil
}
