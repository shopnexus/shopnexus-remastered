// Package id encodes int64 database keys as opaque, keyed, URL-safe strings.
//
// A key is a plain int64 everywhere inside the process (domain, port, adapter).
// Only the published api DTOs use ID[K], whose JSON form is an encrypted,
// kind-prefixed string:
//
//	int64(42) as ID[Listing]  <->  "lst_1ryaj8117v2p4"
//
// The mapping is a keyed permutation of the whole int64 range (see cipher.go),
// so it round-trips exactly without a lookup table. The phantom type parameter
// gives each entity its own prefix and its own cipher tweak — the same number
// under two kinds encodes to two different strings — and lets the compiler
// reject an order id passed where an account id is wanted.
//
// Call SetCipher once at startup, before anything is marshalled.
package id

import (
	"encoding/json/v2"
	"strings"

	"shopnexus/internal/shared/errx"
)

// Kind names one entity family on the wire. Implementations are the empty
// structs in kinds.go; the prefix is part of the public API forever, because
// changing it invalidates every id already handed out.
type Kind interface {
	Prefix() string
}

// ID is a database key of kind K: an int64 in Go and in Postgres, an opaque
// string in JSON. The zero value stands for "no id" and maps to SQL NULL.
type ID[K Kind] int64

// Of wraps a raw database key, at the boundary where a service builds a DTO.
func Of[K Kind](n int64) ID[K] { return ID[K](n) }

// Prefix returns the wire prefix of a kind, for the polymorphic ref_id fields
// whose kind is only known at run time.
func Prefix[K Kind]() string {
	var k K
	return k.Prefix()
}

// Int64 unwraps to the raw database key.
func (i ID[K]) Int64() int64 { return int64(i) }

// String returns the wire form, or "" for the zero id.
func (i ID[K]) String() string {
	if i == 0 {
		return ""
	}
	var k K
	return FormatOpaque(k.Prefix(), int64(i))
}

// Parse decodes a wire-form id of kind K. It rejects a missing or foreign
// prefix, a malformed body, and any value that decodes to <= 0 — identity
// columns start at 1, so a non-positive result cannot be a real key.
func Parse[K Kind](s string) (ID[K], error) {
	var k K
	n, err := ParseOpaque(k.Prefix(), s)
	if err != nil {
		return 0, err
	}
	return ID[K](n), nil
}

// FormatOpaque encodes a key under an explicit prefix. Prefer ID[K].String();
// this is for a polymorphic reference whose kind comes from a sibling ref_type.
func FormatOpaque(prefix string, n int64) string {
	return prefix + "_" + encode(prefix, uint64(n))
}

// ParseOpaque decodes a key under an explicit prefix. Prefer Parse[K]; this is
// for a polymorphic reference whose kind comes from a sibling ref_type.
func ParseOpaque(prefix, s string) (int64, error) {
	body, ok := strings.CutPrefix(s, prefix+"_")
	if !ok {
		return 0, errx.ErrInvalidID
	}
	n, err := decode(prefix, body)
	if err != nil {
		return 0, err
	}
	if int64(n) <= 0 {
		return 0, errx.ErrInvalidID
	}
	return int64(n), nil
}

// MarshalJSON renders the opaque string, or null for the zero id.
func (i ID[K]) MarshalJSON() ([]byte, error) {
	if i == 0 {
		return []byte("null"), nil
	}
	// The encoded form is prefix + '_' + base32, so it never needs escaping.
	return []byte(`"` + i.String() + `"`), nil
}

// UnmarshalJSON accepts the opaque string; null and "" mean the zero id.
func (i *ID[K]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*i = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errx.ErrInvalidID
	}
	return i.UnmarshalText([]byte(s))
}

// MarshalText covers query parameters and map keys.
func (i ID[K]) MarshalText() ([]byte, error) { return []byte(i.String()), nil }

// UnmarshalText covers query parameters and map keys; "" means the zero id.
func (i *ID[K]) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*i = 0
		return nil
	}
	v, err := Parse[K](string(b))
	if err != nil {
		return err
	}
	*i = v
	return nil
}
