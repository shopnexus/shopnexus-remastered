package ordermodel

//go:generate go run shopnexus-server/cmd/genenum -type=Status,DisputeStatus

// Status is the payment session / transaction / transport state, decoupled from the DB enum.
type Status string

const (
	StatusPending    Status = "Pending"
	StatusProcessing Status = "Processing"
	StatusSuccess    Status = "Success"
	StatusCancelled  Status = "Cancelled"
	StatusFailed     Status = "Failed"
)

// DisputeStatus is the refund-dispute resolution state.
type DisputeStatus string

const (
	DisputeStatusOpen       DisputeStatus = "Open"
	DisputeStatusSellerWins DisputeStatus = "SellerWins"
	DisputeStatusBuyerWins  DisputeStatus = "BuyerWins"
)
