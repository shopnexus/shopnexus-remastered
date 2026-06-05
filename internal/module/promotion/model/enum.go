package promotionmodel

//go:generate go run shopnexus-server/cmd/genenum -type=Type,RefType

// Type is the promotion kind, decoupled from the DB enum.
type Type string

const (
	TypeDiscount     Type = "Discount"
	TypeShipDiscount Type = "ShipDiscount"
	TypeBundle       Type = "Bundle"
	TypeBuyXGetY     Type = "BuyXGetY"
	TypeCashback     Type = "Cashback"
)

// RefType is the target a promotion ref points at.
type RefType string

const (
	RefTypeProductSpu RefType = "ProductSpu"
	RefTypeProductSku RefType = "ProductSku"
	RefTypeCategory   RefType = "Category"
)
