package domain

import (
	"encoding/json"
	"time"

	"shopnexus/internal/shared/validation"
)

// Item is one purchased line. It exists from checkout, before the money lands, which is
// what a nil OrderID means: paid-for lines become an order as soon as the payment session
// completes, and nobody confirms anything in between.
//
// Same origin pair as Order: a fixed-price line comes from a checked-out draft, a
// negotiated one from the accepted offer.
type Item struct {
	ID        int64
	DraftID   *int64
	OfferID   *int64
	OrderID   *int64
	BuyerID   int64 `validate:"required"`
	SellerID  int64 `validate:"required"`
	ListingID int64 `validate:"required"`
	VariantID int64 `validate:"required"`
	// Address is the delivery contact copied into the row, not a pointer to one: the
	// administrative codes are what a carrier is called with, and a saved contact may have
	// changed since.
	Address  AddressSnapshot
	Note     string
	Currency string `validate:"required,len=3"`
	Quantity int64  `validate:"required,gt=0"`
	// TransportOption is the carrier the buyer chose. They pay the delivery fee, so it is
	// their trade-off between price and speed.
	TransportOption  string `validate:"required"`
	TotalAmount      int64  `validate:"gte=0"`
	PaymentSessionID int64  `validate:"required"`
	CancelledAt      *time.Time
	CancelledByID    *int64
	CreatedAt        time.Time
}

// AddressSnapshot is a contact frozen into a row. Shaped like account.contact, because
// that is what it was copied from.
type AddressSnapshot struct {
	FullName     string `json:"full_name"`
	Phone        string `json:"phone"`
	Country      string `json:"country"`
	ProvinceCode string `json:"province_code,omitempty"`
	// DistrictCode is null where the country has no district tier, exactly as the contact
	// it was copied from is.
	DistrictCode  *string `json:"district_code,omitempty"`
	WardCode      string  `json:"ward_code,omitempty"`
	AddressDetail *string `json:"address_detail,omitempty"`
}

// Origin says where a line or an order came from. Exactly one side is set, which the
// schema also holds with a CHECK.
type Origin struct {
	DraftID *int64
	OfferID *int64
}

// FromDraft and FromOffer are the two shapes callers build, so a nil pair is not a state
// anybody can construct by accident.
func FromDraft(draftID int64) Origin { return Origin{DraftID: &draftID} }
func FromOffer(offerID int64) Origin { return Origin{OfferID: &offerID} }

// Valid reports whether exactly one side is set.
func (o Origin) Valid() bool {
	return (o.DraftID == nil) != (o.OfferID == nil)
}

func NewItem(origin Origin, buyerID, sellerID, listingID, variantID int64, address AddressSnapshot, note, currency string, quantity int64, transportOption string, total, paymentSessionID int64) (Item, error) {
	if !origin.Valid() {
		return Item{}, ErrVariantNotInDraft
	}
	i := Item{
		DraftID: origin.DraftID, OfferID: origin.OfferID,
		BuyerID: buyerID, SellerID: sellerID, ListingID: listingID, VariantID: variantID,
		Address: address, Note: note, Currency: currency, Quantity: quantity,
		TransportOption: transportOption, TotalAmount: total,
		PaymentSessionID: paymentSessionID,
	}
	if err := validation.Default().Struct(i); err != nil {
		return Item{}, validation.AsError(err)
	}
	return i, nil
}

// Live reports whether the line still counts towards an order.
func (i Item) Live() bool { return i.CancelledAt == nil }

// Cancel voids a line before it becomes an order. After that the buyer asks for a refund
// instead — a decision the seller gets to see.
func (i *Item) Cancel(actorID int64) error {
	if i.OrderID != nil {
		return ErrItemNotCancellable
	}
	if !i.Live() {
		return ErrItemNotCancellable
	}
	i.CancelledAt = new(time.Now())
	if actorID != 0 {
		i.CancelledByID = &actorID
	}
	return nil
}

// EncodeAddress and DecodeAddress are the column's two ends.
func EncodeAddress(a AddressSnapshot) ([]byte, error) { return json.Marshal(a) }

func DecodeAddress(raw []byte) (AddressSnapshot, error) {
	var out AddressSnapshot
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return AddressSnapshot{}, err
	}
	return out, nil
}

// Transport statuses (kebab-case, mirrors the transport_status enum).
const (
	TransportPending   = "pending"
	TransportPickedUp  = "picked-up"
	TransportInTransit = "in-transit"
	TransportDelivered = "delivered"
	TransportReturned  = "returned"
	TransportFailed    = "failed"
)

// Transport is one shipment. The carrier's own view of it lives in Data; this row is what
// the platform knows.
type Transport struct {
	ID     int64
	Option string `validate:"required"`
	Status string `validate:"required"`
	// Fee is what the buyer paid to have this delivered, quoted from the carrier at checkout and
	// frozen here. It is the courier's, not the seller's, which is why the escrow released to a
	// seller never includes it.
	Fee       int64 `validate:"gte=0"`
	Data      []byte
	CreatedAt time.Time
}

// Shipped reports whether the parcel has left: after that an order cannot be cancelled,
// only refunded.
func (t Transport) Shipped() bool {
	return t.Status != TransportPending
}

// Delivered reports whether it arrived — which for a return leg is what closes the leg.
func (t Transport) Delivered() bool { return t.Status == TransportDelivered }

// Settled reports whether the leg has reached an outcome nothing follows.
func (t Transport) Settled() bool {
	return t.Status == TransportDelivered || t.Status == TransportReturned ||
		t.Status == TransportFailed
}

// transportOrder is how far along each status is. Only the legs a parcel passes through are
// ranked; the three outcomes are ends rather than places on the way.
var transportOrder = map[string]int{
	TransportPending:   0,
	TransportPickedUp:  1,
	TransportInTransit: 2,
}

// Advance moves the shipment forward. Forward-only: a carrier's reports can arrive out of
// order, and a parcel that was delivered did not go back to in-transit — nor is `Shipped()`,
// which decides whether an order can still be cancelled, something a later report may undo.
func (t *Transport) Advance(status string) error {
	if t.Settled() {
		return ErrTransportSettled
	}
	switch status {
	case TransportDelivered, TransportReturned, TransportFailed:
		// An outcome is reachable from anywhere: a parcel can fail before it is collected.
		t.Status = status
		return nil
	case TransportPickedUp, TransportInTransit:
		if transportOrder[status] <= transportOrder[t.Status] {
			return ErrTransportSettled
		}
		t.Status = status
		return nil
	default:
		return ErrTransportStatusUnknown
	}
}
