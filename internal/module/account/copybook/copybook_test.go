package copybook_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"shopnexus/internal/module/account/copybook"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/lang"
	"shopnexus/templates"
)

// The real copy, rendered for every kind in every language against the payload its emitter
// actually sends. This is where the words are checked; a service test uses a fake so it asserts
// on wiring instead.
//
// paramsByKind is the other half of the contract templates/notification/*.yaml keeps: a template
// naming a key nobody sends fails to render, and a fixture list that has drifted from the
// vocabulary is what would let that reach a reader instead of this test.
var paramsByKind = map[domain.Kind]map[string]any{
	domain.KindOrderPlaced:      {"order_id": "ord_x", "total": int64(1250000), "currency": "VND"},
	domain.KindSaleReceived:     {"order_id": "ord_x", "total": int64(1250000), "currency": "VND"},
	domain.KindOrderDelivered:   {"order_id": "ord_x"},
	domain.KindSaleHandedOver:   {"order_id": "ord_x"},
	domain.KindOrderCompleted:   {"order_id": "ord_x"},
	domain.KindSaleCompleted:    {"order_id": "ord_x"},
	domain.KindOrderCancelled:   {"order_id": "ord_x"},
	domain.KindSaleCancelled:    {"order_id": "ord_x"},
	domain.KindOrderUnconfirmed: {"order_id": "ord_x"},
	domain.KindSaleUnconfirmed:  {"order_id": "ord_x"},
	domain.KindRefundEscalated:  {"order_id": "ord_x"},

	domain.KindRefundResolved:     {"order_id": "ord_x", "buyer_wins": true, "note": "hàng không đúng mô tả"},
	domain.KindSaleRefundResolved: {"order_id": "ord_x", "buyer_wins": false, "note": ""},

	domain.KindOfferCountered: {"listing_name": "Máy ảnh Canon", "price": int64(4500000), "currency": "VND"},
	domain.KindOfferAccepted:  {"listing_name": "Máy ảnh Canon", "price": int64(4500000), "currency": "VND"},
	domain.KindOfferWithdrawn: {"listing_name": "Máy ảnh Canon"},

	domain.KindListingApproved:  {"listing_id": "lst_x", "listing_name": "Áo khoác", "reason": ""},
	domain.KindListingTakenDown: {"listing_id": "lst_x", "listing_name": "Áo khoác", "reason": "hàng giả"},

	domain.KindWithdrawalPaid:  {"amount": int64(900000), "currency": "VND"},
	domain.KindPayoutFailed:    {},
	domain.KindCheckoutExpired: {},

	domain.KindNewFollower: {"follower_id": "acc_x", "follower_name": "Bùi"},
}

func book(t *testing.T) *copybook.Book {
	t.Helper()
	b, err := copybook.Load(templates.Notification())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return b
}

// Every kind renders a title in every language, out of the payload its emitter sends. A hole in
// the copy is a blank line in somebody's feed, and `<no value>` is what a nil parameter looks
// like once it is already on their screen.
func TestRender_EveryKindInEveryLanguage(t *testing.T) {
	b := book(t)
	if len(paramsByKind) != len(domain.Kinds) {
		t.Fatalf("%d fixtures for %d kinds — one of the two lists has an entry the other does not",
			len(paramsByKind), len(domain.Kinds))
	}
	for _, kind := range domain.Kinds {
		params, ok := paramsByKind[kind]
		if !ok {
			t.Fatalf("kind %q has no fixture — add it beside the others when adding the kind", kind)
		}
		for _, l := range lang.All {
			title, body := b.Render(l, kind, params)
			if title == "" {
				t.Errorf("Render(%s, %s): empty title", l, kind)
			}
			for _, s := range []string{title, body} {
				if strings.Contains(s, "<no value>") || strings.Contains(s, "{{") {
					t.Errorf("Render(%s, %s): unrendered copy %q", l, kind, s)
				}
			}
		}
	}
}

// A verdict reads differently depending on which way it went, and the two sides read differently
// again — four sentences from one fact. Rendering the same words for a granted and a declined
// refund is the version of this bug that reaches the person it is worst for.
func TestRender_VerdictBranches(t *testing.T) {
	b := book(t)
	seen := map[string]bool{}
	for _, kind := range []domain.Kind{domain.KindRefundResolved, domain.KindSaleRefundResolved} {
		for _, wins := range []bool{true, false} {
			title, _ := b.Render(lang.VI, kind, map[string]any{
				"order_id": "ord_x", "buyer_wins": wins, "note": "",
			})
			if seen[title] {
				t.Errorf("kind %q with buyer_wins=%v reads the same as another case: %q", kind, wins, title)
			}
			seen[title] = true
		}
	}
}

// An amount is stored unscaled and read by a person, so it is grouped — the way the language of
// the row does it, which is the whole reason the helper is bound to the template's language.
func TestRender_MoneyGroupsPerLanguage(t *testing.T) {
	b := book(t)
	for l, want := range map[string]string{lang.VI: "1.250.000 ₫", lang.EN: "1,250,000 ₫"} {
		_, body := b.Render(l, domain.KindOrderPlaced, paramsByKind[domain.KindOrderPlaced])
		if !strings.Contains(body, want) {
			t.Errorf("body in %s = %q, want it to contain %q", l, body, want)
		}
	}
}

// The locale is BCP 47 and the files are per language, so `vi-VN` reads Vietnamese and anything
// unknown falls back rather than rendering nothing.
func TestRender_LocaleFallsBack(t *testing.T) {
	b := book(t)
	vi, _ := b.Render("vi-VN", domain.KindOrderPlaced, paramsByKind[domain.KindOrderPlaced])
	viBase, _ := b.Render(lang.VI, domain.KindOrderPlaced, paramsByKind[domain.KindOrderPlaced])
	if vi != viBase {
		t.Errorf("vi-VN = %q, want the same as vi (%q)", vi, viBase)
	}
	if got, _ := b.Render("kl-GL", domain.KindOrderPlaced, paramsByKind[domain.KindOrderPlaced]); got == "" {
		t.Error("an unknown locale rendered nothing; it must fall back")
	}
}

// A payload key the emitter forgot must cost that row its subtitle and nothing else. The feed is
// a page of rows, and failing the read over one missing total is worse than the hole.
func TestRender_MissingKeyCostsOnlyThatLine(t *testing.T) {
	b := book(t)
	title, body := b.Render(lang.VI, domain.KindOrderPlaced, map[string]any{"order_id": "ord_x"})
	if title == "" {
		t.Error("the title asks for no total and should still render")
	}
	if body != "" {
		t.Errorf("body = %q, want empty: it names a total the caller did not send", body)
	}
}

// Load is the startup gate. A kind with no copy, or copy for a kind nobody knows, has to be a
// process that does not come up — the alternative is discovering it in somebody's feed.
func TestLoad_RefusesAnIncompleteBook(t *testing.T) {
	complete := "order-placed:\n  title: \"t\"\n"
	for name, files := range map[string]fstest.MapFS{
		"a language is missing": {"vi.yaml": &fstest.MapFile{Data: []byte(complete)}},
		"a kind has no copy": {
			"vi.yaml": &fstest.MapFile{Data: []byte(complete)},
			"en.yaml": &fstest.MapFile{Data: []byte(complete)},
		},
		"a kind nobody knows": {
			"vi.yaml": &fstest.MapFile{Data: []byte("order-vanished:\n  title: \"t\"\n")},
			"en.yaml": &fstest.MapFile{Data: []byte("order-vanished:\n  title: \"t\"\n")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := copybook.Load(files); err == nil {
				t.Fatal("Load accepted a book it should have refused")
			}
		})
	}
}
