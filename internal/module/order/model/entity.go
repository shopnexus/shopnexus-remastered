package ordermodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
)

//pgtempl:table "order"."cart_item"
type CartItem struct {
	ID        int64     `db:"id"`
	AccountID uuid.UUID `db:"account_id"`
	SkuID     uuid.UUID `db:"sku_id"`
	Quantity  int64     `db:"quantity"`
}

//pgtempl:table "order"."payment_session"
type PaymentSession struct {
	ID          uuid.UUID       `db:"id"`
	Kind        string          `db:"kind"`
	Status      Status          `db:"status"`
	FromID      uuid.NullUUID   `db:"from_id"`
	ToID        uuid.NullUUID   `db:"to_id"`
	Note        string          `db:"note"`
	Currency    string          `db:"currency"`
	TotalAmount int64           `db:"total_amount"`
	FxSnapshot  json.RawMessage `db:"fx_snapshot"`
	Data        json.RawMessage `db:"data"`
	DateCreated time.Time       `db:"date_created"`
	DatePaid    null.Time       `db:"date_paid"`
	DateExpired time.Time       `db:"date_expired"`
}

//pgtempl:table "order"."transaction"
type Transaction struct {
	ID            uuid.UUID       `db:"id"`
	SessionID     uuid.UUID       `db:"session_id"`
	Status        Status          `db:"status"`
	Note          string          `db:"note"`
	Error         null.String     `db:"error"`
	PaymentOption null.String     `db:"payment_option"`
	Data          json.RawMessage `db:"data"`
	Amount        int64           `db:"amount"`
	Currency      string          `db:"currency"`
	ReversesID    uuid.NullUUID   `db:"reverses_id"`
	DateCreated   time.Time       `db:"date_created"`
	DateSettled   null.Time       `db:"date_settled"`
	DateExpired   null.Time       `db:"date_expired"`
}

//pgtempl:table "order"."transport"
type Transport struct {
	ID          int64           `db:"id"`
	Option      string          `db:"option"`
	Status      null.String     `db:"status"`
	Data        json.RawMessage `db:"data"`
	DateCreated time.Time       `db:"date_created"`
}
