package transport

type Status string

// kebab-case, like every other enum-like value in this codebase.
const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSuccess    Status = "success"
	StatusCancelled  Status = "cancelled"
	StatusFailed     Status = "failed"
)
