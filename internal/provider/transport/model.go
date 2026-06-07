package transport

type Status string

const (
	StatusPending    Status = "Pending"
	StatusProcessing Status = "Processing"
	StatusSuccess    Status = "Success"
	StatusCancelled  Status = "Cancelled"
	StatusFailed     Status = "Failed"
)
