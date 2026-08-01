package common

// The three kinds of change an audit row records. The column is a plain string, so the value has
// to be named somewhere: here, next to the field, rather than at each of the five writers.
const (
	ChangeTypeInsert = "insert"
	ChangeTypeUpdate = "update"
	ChangeTypeDelete = "delete"
)

// AuditEntry is one row of a module's audit log. A module's Save writes these itself from the
// aggregate's events; dbx.InsertAuditLog is the write.
//
// Declared here rather than in each module's port, where two copies had already appeared.
type AuditEntry struct {
	Table      string
	RecordID   int64
	ChangeType string // one of the ChangeType* constants above
	// Code is the business event, e.g. "listing.publish".
	Code string
	// ChangedBy is nil for a change no account is responsible for (a scheduled job, a vendor
	// callback).
	ChangedBy *int64
	// Diff and Snapshot are whatever the recorder declared — a domain event's payload and a
	// row snapshot — and reach the JSONB columns through json.Marshal. `any` because the shape
	// belongs to the fact, not to the trail.
	Diff     any
	Snapshot any
}
