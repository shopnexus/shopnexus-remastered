package id_test

import (
	"encoding/json/v2"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// testKey is fixed so the stability test below can pin exact output.
var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestMain(m *testing.M) {
	if err := id.SetCipher(testKey); err != nil {
		panic(err)
	}
	m.Run()
}

func TestSetCipher_RejectsBadKeyLength(t *testing.T) {
	if err := id.SetCipher([]byte("too-short")); err == nil {
		t.Fatal("expected an error for a 9-byte key")
	}
	// Restore the key the rest of the suite relies on.
	if err := id.SetCipher(testKey); err != nil {
		t.Fatalf("restore cipher: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	values := []int64{1, 2, 3, 41, 42, 255, 256, 1 << 20, 1 << 40, math.MaxInt32, math.MaxInt64}
	for i := int64(4); i < 2000; i++ {
		values = append(values, i)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 10000 {
		values = append(values, rng.Int64N(math.MaxInt64)+1)
	}

	for _, want := range values {
		s := id.Of[id.Listing](want).String()
		got, err := id.Parse[id.Listing](s)
		if err != nil {
			t.Fatalf("Parse(%q) for %d: %v", s, want, err)
		}
		if got.Int64() != want {
			t.Fatalf("round trip %d -> %q -> %d", want, s, got.Int64())
		}
	}
}

func TestString_ShapeAndLength(t *testing.T) {
	s := id.Of[id.Account](42).String()
	body, ok := strings.CutPrefix(s, "acc_")
	if !ok {
		t.Fatalf("%q is missing the acc_ prefix", s)
	}
	if len(body) != 13 {
		t.Fatalf("body %q has length %d, want 13", body, len(body))
	}
	const allowed = "0123456789abcdefghjkmnpqrstvwxyz"
	if strings.ContainsFunc(body, func(r rune) bool { return !strings.ContainsRune(allowed, r) }) {
		t.Fatalf("body %q has a character outside the alphabet", body)
	}
}

// The encoding must be a permutation: no two keys may collide.
func TestNoCollisions(t *testing.T) {
	seen := make(map[string]int64, 50000)
	for n := int64(1); n <= 50000; n++ {
		s := id.Of[id.Order](n).String()
		if prev, dup := seen[s]; dup {
			t.Fatalf("%q collides: %d and %d", s, prev, n)
		}
		seen[s] = n
	}
}

func TestKindSeparation(t *testing.T) {
	const n = 42
	acc := id.Of[id.Account](n).String()
	ord := id.Of[id.Order](n).String()

	accBody := strings.TrimPrefix(acc, "acc_")
	ordBody := strings.TrimPrefix(ord, "ord_")
	if accBody == ordBody {
		t.Fatalf("kinds must not share an encoding for %d: %q", n, accBody)
	}
	if _, err := id.Parse[id.Account](ord); err == nil {
		t.Fatalf("parsing an order id as an account id must fail: %q", ord)
	}
}

func TestParse_Rejects(t *testing.T) {
	valid := id.Of[id.Listing](42).String()
	body := strings.TrimPrefix(valid, "lst_")

	cases := map[string]string{
		"empty":            "",
		"no prefix":        body,
		"foreign prefix":   "acc_" + body,
		"prefix only":      "lst_",
		"missing sep":      "lst" + body,
		"too short":        "lst_" + body[:12],
		"too long":         "lst_" + body + "0",
		"letter u":         "lst_uuuuuuuuuuuuu",
		"character out":    "lst_" + body[:12] + "-",
		"overflows uint64": "lst_zzzzzzzzzzzzz",
	}
	for name, s := range cases {
		if _, err := id.Parse[id.Listing](s); err == nil {
			t.Errorf("%s: Parse(%q) must fail", name, s)
		} else if status, code, _, ok := errx.Decompose(err); !ok || status != 400 || code != "invalid_id" {
			t.Errorf("%s: want 400/invalid_id, got %v", name, err)
		}
	}
}

// A key that decodes to <= 0 cannot be a real identity value, so it is rejected
// before it ever reaches the database.
func TestParse_RejectsNonPositive(t *testing.T) {
	for _, n := range []int64{0, -1, math.MinInt64} {
		s := "lst_" + strings.TrimPrefix(id.Of[id.Listing](n).String(), "lst_")
		if n == 0 {
			// The zero id has no wire form; encode it through the escape hatch.
			s = id.FormatOpaque(id.Prefix[id.Listing](), 0)
		}
		if _, err := id.Parse[id.Listing](s); err == nil {
			t.Errorf("Parse of %d (%q) must fail", n, s)
		}
	}
}

// Crockford folding: an id typed by hand still resolves.
func TestParse_FoldsCaseAndAliases(t *testing.T) {
	want := id.Of[id.Account](987654321)
	s := want.String()
	body := strings.TrimPrefix(s, "acc_")

	variants := []string{
		"acc_" + strings.ToUpper(body),
		"acc_" + strings.ReplaceAll(body, "1", "l"),
		"acc_" + strings.ReplaceAll(body, "1", "I"),
		"acc_" + strings.ReplaceAll(body, "0", "o"),
		"acc_" + strings.ReplaceAll(body, "0", "O"),
	}
	for _, v := range variants {
		got, err := id.Parse[id.Account](v)
		if err != nil {
			t.Errorf("Parse(%q): %v", v, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", v, got.Int64(), want.Int64())
		}
	}
}

func TestZero(t *testing.T) {
	var zero id.ID[id.Account]
	if zero.String() != "" {
		t.Fatalf("zero String() = %q, want empty", zero.String())
	}
	b, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("zero marshals to %s, want null", b)
	}
}

type payload struct {
	ID      id.ID[id.Listing] `json:"id"`
	OwnerID id.ID[id.Account] `json:"owner_id"`
	Parent  id.ID[id.Listing] `json:"parent"`
}

func TestJSON_RoundTrip(t *testing.T) {
	in := payload{ID: id.Of[id.Listing](7), OwnerID: id.Of[id.Account](7)}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Same number, two kinds -> two different strings; the zero id -> null.
	if strings.Count(string(b), in.ID.String()) != 1 || !strings.Contains(string(b), in.OwnerID.String()) {
		t.Fatalf("unexpected json: %s", b)
	}
	if !strings.Contains(string(b), `"parent":null`) {
		t.Fatalf("zero id must marshal to null: %s", b)
	}

	var out payload
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestJSON_RejectsMalformed(t *testing.T) {
	for _, body := range []string{`{"id":"lst_nope"}`, `{"id":"acc_2h9qk4mfx7bd3"}`, `{"id":42}`} {
		var out payload
		if err := json.Unmarshal([]byte(body), &out); err == nil {
			t.Errorf("Unmarshal(%s) must fail", body)
		}
	}
}

func TestJSON_NullAndEmptyMeanZero(t *testing.T) {
	for _, body := range []string{`{"id":null}`, `{"id":""}`} {
		var out payload
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("Unmarshal(%s): %v", body, err)
		}
		if out.ID != 0 {
			t.Fatalf("Unmarshal(%s) = %d, want 0", body, out.ID.Int64())
		}
	}
}

func TestText_RoundTrip(t *testing.T) {
	want := id.Of[id.Message](12345)
	b, err := want.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	var got id.ID[id.Message]
	if err := got.UnmarshalText(b); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if got != want {
		t.Fatalf("got %d, want %d", got.Int64(), want.Int64())
	}
	var zero id.ID[id.Message]
	if err := zero.UnmarshalText(nil); err != nil || zero != 0 {
		t.Fatalf("empty text must decode to the zero id, got %d, %v", zero.Int64(), err)
	}
}

func TestOpaque_MatchesTypedForm(t *testing.T) {
	// The polymorphic escape hatch must agree with the typed API.
	const n = 55
	typed := id.Of[id.Message](n).String()
	opaque := id.FormatOpaque(id.Prefix[id.Message](), n)
	if typed != opaque {
		t.Fatalf("FormatOpaque = %q, typed = %q", opaque, typed)
	}
	got, err := id.ParseOpaque(id.Prefix[id.Message](), typed)
	if err != nil {
		t.Fatalf("ParseOpaque: %v", err)
	}
	if got != n {
		t.Fatalf("ParseOpaque = %d, want %d", got, n)
	}
}

func TestPrefixesAreUniqueAndWellFormed(t *testing.T) {
	prefixes := []string{
		id.Prefix[id.Account](), id.Prefix[id.Contact](), id.Prefix[id.Category](),
		id.Prefix[id.Listing](), id.Prefix[id.Variant](), id.Prefix[id.Order](),
		id.Prefix[id.Item](), id.Prefix[id.Refund](),
		id.Prefix[id.Offer](), id.Prefix[id.PaymentSession](), id.Prefix[id.Transaction](),
		id.Prefix[id.BankAccount](), id.Prefix[id.Feedback](), id.Prefix[id.Review](),
		id.Prefix[id.ReviewReply](), id.Prefix[id.Ticket](),
		id.Prefix[id.Conversation](), id.Prefix[id.Message](), id.Prefix[id.Resource](),
	}
	seen := map[string]bool{}
	for _, p := range prefixes {
		if seen[p] {
			t.Errorf("duplicate prefix %q", p)
		}
		seen[p] = true
		if len(p) != 3 || strings.ToLower(p) != p {
			t.Errorf("prefix %q must be three lowercase letters", p)
		}
	}
}

// Stability: with a fixed key the wire form must not drift. If this fails, the
// cipher or the alphabet changed and every id already handed out is now invalid.
func TestStability_GoldenVectors(t *testing.T) {
	want := map[string]string{
		"acc:1":   "acc_3pb2yypj6z4pj",
		"acc:2":   "acc_06jh8rqsvf19t",
		"acc:42":  "acc_62mxefynht57b",
		"lst:1":   "lst_8fd46etc0b7ex",
		"lst:42":  "lst_5y5w68r4918v8",
		"ord:42":  "ord_fv2cpg50vkrfp",
		"msg:999": "msg_agnc1pe4pjb4k",
	}
	got := map[string]string{
		"acc:1":   id.Of[id.Account](1).String(),
		"acc:2":   id.Of[id.Account](2).String(),
		"acc:42":  id.Of[id.Account](42).String(),
		"lst:1":   id.Of[id.Listing](1).String(),
		"lst:42":  id.Of[id.Listing](42).String(),
		"ord:42":  id.Of[id.Order](42).String(),
		"msg:999": id.Of[id.Message](999).String(),
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}
