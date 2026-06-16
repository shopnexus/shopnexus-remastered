package orderrepo

import (
	"context"
	"encoding/json"

	null "github.com/guregu/null/v6"
	ordermodel "shopnexus-server/internal/module/order/model"
)

const getTransportByTrackingID = `SELECT id, option, status, data, date_created FROM "order"."transport"
WHERE "data"->>'tracking_id' = $1::text
LIMIT 1`

func (r *Repository) GetTransportByTrackingID(ctx context.Context, trackingID string) (ordermodel.Transport, error) {
	row := r.db.QueryRow(ctx, getTransportByTrackingID, trackingID)
	var i ordermodel.Transport
	err := row.Scan(&i.ID, &i.Option, &i.Status, &i.Data, &i.DateCreated)
	return i, err
}

const getTransportWithOrder = `SELECT t.id, t.option, t.status, t.data, t.date_created,
       o.id        AS order_id,
       o.buyer_id  AS order_buyer_id,
       o.seller_id AS order_seller_id
FROM "order"."transport" t
INNER JOIN "order"."order" o ON o.transport_id = t.id
WHERE t.id = $1`

func (r *Repository) GetTransportWithOrder(ctx context.Context, id int64) (ordermodel.TransportWithOrder, error) {
	row := r.db.QueryRow(ctx, getTransportWithOrder, id)
	var i ordermodel.TransportWithOrder
	err := row.Scan(
		&i.ID,
		&i.Option,
		&i.Status,
		&i.Data,
		&i.DateCreated,
		&i.OrderID,
		&i.OrderBuyerID,
		&i.OrderSellerID,
	)
	return i, err
}

const updateTransportStatusByID = `UPDATE "order"."transport"
SET "status" = $1, "data" = $2
WHERE "id" = $3
RETURNING id, option, status, data, date_created`

// UpdateTransportStatusByIDParams holds the mutable fields for UpdateTransportStatusByID.
type UpdateTransportStatusByIDParams struct {
	Status null.String     `json:"status"`
	Data   json.RawMessage `json:"data"`
	ID     int64           `json:"id"`
}

func (r *Repository) UpdateTransportStatusByID(ctx context.Context, arg UpdateTransportStatusByIDParams) (ordermodel.Transport, error) {
	row := r.db.QueryRow(ctx, updateTransportStatusByID, arg.Status, arg.Data, arg.ID)
	var i ordermodel.Transport
	err := row.Scan(&i.ID, &i.Option, &i.Status, &i.Data, &i.DateCreated)
	return i, err
}

