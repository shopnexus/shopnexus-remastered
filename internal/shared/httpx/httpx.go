// Package httpx: JSON helpers + errx -> HTTP mapping. The only place that knows HTTP status.
package httpx

import (
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"

	"shopnexus/internal/shared/errx"
)

// This package marshals with encoding/json/v2, and the reason is the wire shape: v1 renders a
// nil slice as `null`, so every collection route answered `null` for "none" and each client had
// to defend against a second empty value. v2 renders it `[]`, which is what the spec claims and
// what a generated client's `T[]` already expects.
func DecodeJSON(r *http.Request, dst any) error {
	return json.UnmarshalRead(r.Body, dst, json.RejectUnknownMembers(true))
}

// DecodeVendorJSON reads a third party's JSON — a provider's response, or its webhook.
//
// It keeps v1's case-insensitive name matching, which our own routes deliberately do not: a
// vendor's casing is whatever their documentation happened to say (eSMS answers "CodeResult"
// beside "SMSID"), v2 matches names exactly, and a name that does not match is dropped
// *silently* rather than refused. There is no sandbox for most of these to discover that in,
// and a field that quietly reads as zero is how a settled payment becomes an unsettled one.
func DecodeVendorJSON(r io.Reader, dst any) error {
	return json.UnmarshalRead(r, dst, json.MatchCaseInsensitiveNames(true))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}

// RequestIDHeader is where the request id travels. The logging middleware sets it on the
// way in, so it is on every response — including a 204 and anything non-JSON — and
// WriteError reads it back off the writer to put in the body.
const RequestIDHeader = "X-Request-Id"

// The response envelope. Every JSON body this package writes has "data" or "error" at the
// root and never both, plus "meta" beside "data" for a paginated read.
//
// A payload is never the root object. Returning it bare makes its own fields ambiguous
// with the envelope's, and that is not theoretical in this API: a Transaction has an
// "error" field carrying the rail's failure text, so a successful 201 and a gateway
// failure would be the same shape to the near-universal client pattern of "if
// (body.error) throw". An Order and a CheckoutResult have "items", an Option has "data".
// One level of nesting makes all of those impossible rather than merely unlikely.
type dataEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

type errEnvelope struct {
	Error errBody `json:"error"`
}

type errBody struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	RequestID string       `json:"request_id"`
	Fields    []errx.Field `json:"fields"`
}

// PageMeta accompanies a page-paginated collection. TotalCount is a pointer because null
// is a real answer: a ranked query never visits the rows it did not return, so it has no
// total, and a client has to draw "more results" instead of "page 3 of 12".
type PageMeta struct {
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
	TotalCount *int64 `json:"total_count"`
}

// CursorMeta accompanies a cursor-paginated collection. NextCursor is a pointer so the
// last page says null rather than omitting the key.
type CursorMeta struct {
	NextCursor *string `json:"next_cursor"`
}

// WriteData writes one resource: {"data": …}.
func WriteData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, dataEnvelope{Data: data})
}

// WritePage writes a page-paginated collection: {"data": […], "meta": {page, limit, total_count}}.
func WritePage(w http.ResponseWriter, status int, data any, meta PageMeta) {
	writeJSON(w, status, dataEnvelope{Data: data, Meta: meta})
}

// WriteCursor writes a cursor-paginated collection: {"data": […], "meta": {next_cursor}}.
func WriteCursor(w http.ResponseWriter, status int, data any, meta CursorMeta) {
	writeJSON(w, status, dataEnvelope{Data: data, Meta: meta})
}

// WriteNoContent answers 204 with no body — a delete, or a state change with nothing to
// report. Deliberately not an empty envelope: there is no data, and {"data": null} would
// invite a client to read it.
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func WriteError(w http.ResponseWriter, log *slog.Logger, err error) {
	reqID := w.Header().Get(RequestIDHeader)
	if status, code, message, ok := errx.Decompose(err); ok {
		writeJSON(w, int(status), errEnvelope{Error: errBody{
			Code:      code,
			Message:   message,
			RequestID: reqID,
			Fields:    errx.FieldsOf(err),
		}})
		return
	}
	// An uncoded error is a bug, not a business outcome: log it whole and tell the caller
	// nothing but the request id, which is the only thing that helps either of us.
	log.Error("unhandled error", "err", err, "request_id", reqID)
	writeJSON(w, http.StatusInternalServerError, errEnvelope{Error: errBody{
		Code:      "internal",
		Message:   "internal error",
		RequestID: reqID,
	}})
}
