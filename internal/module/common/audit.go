package common

// AuditEntry is one row of a module's audit log. A module's Save writes these itself from the
// aggregate's events; dbx.InsertAuditLog is the write.
//
// Declared here rather than in each module's port, where two copies had already appeared.
type AuditEntry struct {
	Table      string
	RecordID   int64
	ChangeType string
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
