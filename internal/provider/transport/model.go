package transport

// Status is a carrier's own vocabulary for where a parcel is. Order translates it into the
// module's shipment statuses (`RecordCarrierCheckpoint`), which is the only reader.
type Status string

const (
	StatusProcessing Status = "processing"
	StatusSuccess    Status = "success"
	StatusFailed     Status = "failed"
)
