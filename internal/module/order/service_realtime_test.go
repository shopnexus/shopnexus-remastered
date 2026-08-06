package order_test

import (
	"encoding/json/v2"
	"errors"
	"sync"
	"testing"

	"shopnexus/internal/module/order"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/shared/realtime"
)

// offerRecorder captures every push a command made, so a test asserts on recipients and
// codes without a bus.
type offerRecorder struct {
	mu   sync.Mutex
	sent []offerPush
}

type offerPush struct {
	subject string
	env     realtime.Envelope
}

func (r *offerRecorder) Broadcast(subject string, b []byte) error {
	var env realtime.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, offerPush{subject: subject, env: env})
	return nil
}

func (r *offerRecorder) OnBroadcast(string, func([]byte)) (func(), error) { return func() {}, nil }

// subjectSet is the set of subjects a command pushed to, order-independent: both parties
// are notified but not in any promised order.
func (r *offerRecorder) subjectSet() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]bool, len(r.sent))
	for _, p := range r.sent {
		out[p.subject] = true
	}
	return out
}

func wantBothParties() map[string]bool {
	return map[string]bool{
		realtime.AccountSubject(buyer.Int64()):  true,
		realtime.AccountSubject(seller.Int64()): true,
	}
}

// requireBothPartiesNotified fails unless exactly buyer and seller were pushed to, each
// carrying order.offer_updated.
func requireBothPartiesNotified(t *testing.T, rec *offerRecorder) {
	t.Helper()
	got := rec.subjectSet()
	want := wantBothParties()
	if len(got) != len(want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
	for s := range want {
		if !got[s] {
			t.Errorf("subjects = %v, missing %v", got, s)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, p := range rec.sent {
		if p.env.Code != order.OfferUpdated.Code {
			t.Errorf("code = %q, want %q", p.env.Code, order.OfferUpdated.Code)
		}
	}
}

// Either side of a negotiation may cause the change the other is watching for, so a
// counter reaches both — not just the side that did not make it.
func TestCounterOfferNotifiesBothParties(t *testing.T) {
	rec := &offerRecorder{}
	h := newHarnessWithFanout("negotiable", rec)
	ctx := t.Context()

	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	// CreateOffer does not go through SaveOffer, so nothing was pushed by it.
	if got := rec.subjectSet(); len(got) != 0 {
		t.Fatalf("subjects after CreateOffer = %v, want none", got)
	}

	if _, err := h.svc.CounterOffer(ctx, orderapi.CounterOfferRequest{
		ActorID: seller, ID: offer.ID, Quantity: 1, Total: 90_000,
	}); err != nil {
		t.Fatalf("CounterOffer: %v", err)
	}
	requireBothPartiesNotified(t, rec)
}

// Accepting is not the sale, but both sides are watching the negotiation regardless of
// who agreed.
func TestAcceptOfferNotifiesBothParties(t *testing.T) {
	rec := &offerRecorder{}
	h := newHarnessWithFanout("negotiable", rec)
	ctx := t.Context()

	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	if _, err := h.svc.AcceptOffer(ctx, orderapi.OfferRequest{ActorID: seller, ID: offer.ID}); err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	requireBothPartiesNotified(t, rec)
}

// A withdrawal is as much a change to the standing terms as a counter, so it is pushed
// the same way.
func TestCancelOfferNotifiesBothParties(t *testing.T) {
	rec := &offerRecorder{}
	h := newHarnessWithFanout("negotiable", rec)
	ctx := t.Context()

	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	if err := h.svc.CancelOffer(ctx, orderapi.OfferRequest{ActorID: buyer, ID: offer.ID}); err != nil {
		t.Fatalf("CancelOffer: %v", err)
	}
	requireBothPartiesNotified(t, rec)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, p := range rec.sent {
		var dto orderapi.Offer
		if err := json.Unmarshal(p.env.Data, &dto); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if dto.Status != domain.OfferCancelled {
			t.Errorf("status = %q, want %q", dto.Status, domain.OfferCancelled)
		}
	}
}

// A push failure must not fail the command: the row is already committed, so the caller
// still gets its answer and the socket is briefly stale.
func TestCounterOfferSucceedsWhenTheBusIsDown(t *testing.T) {
	h := newHarnessWithFanout("negotiable", failingFanout{})
	ctx := t.Context()

	offer, err := h.svc.CreateOffer(ctx, orderapi.CreateOfferRequest{
		ActorID: buyer, VariantID: variantID, Quantity: 1, Total: 80_000,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if _, err := h.svc.CounterOffer(ctx, orderapi.CounterOfferRequest{
		ActorID: seller, ID: offer.ID, Quantity: 1, Total: 90_000,
	}); err != nil {
		t.Fatalf("CounterOffer: %v — a realtime failure must not fail the write", err)
	}
}

type failingFanout struct{}

func (failingFanout) Broadcast(string, []byte) error { return errors.New("nats down") }
func (failingFanout) OnBroadcast(string, func([]byte)) (func(), error) {
	return func() {}, nil
}
