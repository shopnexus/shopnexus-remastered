// Package handler holds the thin HTTP handlers. A handler decodes and validates the request,
// calls the module's api.Service, and writes the result; it holds no business logic of its own.
//
// This file is the shared plumbing every handler goes through — the actor, the path and query
// parameters, the body decode and the validation — so a limit that means 20 on one route and 50 on
// another is not a bug a client finds in production.
package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/httpx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// failed writes err and reports whether the request is over. It exists so a handler reads
// as the four steps above rather than as error plumbing with steps buried in it.
func failed(w http.ResponseWriter, log *slog.Logger, err error) bool {
	if err == nil {
		return false
	}
	httpx.WriteError(w, log, err)
	return true
}

// Request-plumbing shared by every handler: who is calling, which resource, which page.
// It lives here rather than in each handler because the answers have to be identical
// across the API — a limit that means 20 on one route and 50 on another is a bug a
// client discovers in production.

// Pagination defaults, which mirror the ones the OpenAPI base document declares. A
// client that sends nothing gets the same page the spec promises.
const (
	defaultPage  = 1
	defaultLimit = 20
	maxLimit     = 100
)

// actor is the signed-in caller. It exists because a handler behind the auth middleware
// must never fall back to an id from the body: the middleware is the only source, and a
// missing one is a wiring mistake rather than a client error.
func actor(r *http.Request) (id.ID[id.Account], error) {
	uid, ok := gwctx.UserID(r.Context())
	if !ok {
		return 0, errx.ErrUnauthorized
	}
	return uid, nil
}

// pathID decodes an opaque id from a path segment. The kind is a type parameter, so a
// contact id cannot be read out of a route that carries a device id.
func pathID[K id.Kind](r *http.Request, name string) (id.ID[K], error) {
	return id.Parse[K](r.PathValue(name))
}

// pageParams reads the page-paginated query. Out-of-range values are rejected rather
// than clamped: a client asking for 500 rows has a bug, and silently answering with 100
// hides it.
func pageParams(r *http.Request) (page, limit int, err error) {
	page, err = intParam(r, "page", defaultPage, 1, 0)
	if err != nil {
		return 0, 0, err
	}
	limit, err = intParam(r, "limit", defaultLimit, 1, maxLimit)
	if err != nil {
		return 0, 0, err
	}
	return page, limit, nil
}

// limitParam is pageParams' cursor-paginated half: a stream has a limit and a cursor,
// and no page number to jump to.
func limitParam(r *http.Request) (int, error) {
	return intParam(r, "limit", defaultLimit, 1, maxLimit)
}

// intParam parses one bounded integer query parameter. max = 0 means unbounded.
func intParam(r *http.Request, name string, def, min, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || (max > 0 && v > max) {
		return 0, errx.NewValidationError("invalid field: "+name, errx.Field{
			Field:   name,
			Rule:    "range",
			Message: rangeMessage(min, max),
		})
	}
	return v, nil
}

// cursorParam is the opaque page marker a list route continues from. Through here rather than read
// at each of thirteen call sites, for the same reason limitParam is: one spelling of the key.
func cursorParam(r *http.Request) string { return r.URL.Query().Get("cursor") }

// stringParam is a query value with a default, for the routes that filter on a fixed vocabulary.
func stringParam(r *http.Request, name, def string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return def
}

func rangeMessage(min, max int) string {
	if max > 0 {
		return "must be an integer between " + strconv.Itoa(min) + " and " + strconv.Itoa(max)
	}
	return "must be an integer of at least " + strconv.Itoa(min)
}

// boolParam reads a tri-state flag: absent is not the same request as false. "unread=false"
// asks for the whole feed, and no unread parameter at all asks for the same thing for a
// different reason — but a filter the service can see is what keeps that decision in one
// place.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errx.NewValidationError("invalid field: "+name, errx.Field{
			Field: name, Rule: "boolean", Message: "must be true or false",
		})
	}
	return &v, nil
}

// int64Param reads an optional numeric bound. Absent is nil, not zero: "min_price=0" and no
// min_price at all are the same answer here, but the filter that decides that is the service's.
func int64Param(r *http.Request, name string) (*int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, errx.NewValidationError("invalid field: "+name, errx.Field{
			Field: name, Rule: "numeric", Message: "must be an integer",
		})
	}
	return &v, nil
}

// floatParam is int64Param for a coordinate or a radius: absent stays absent, so the service can
// tell "the buyer sent no position" from "the buyer is at the equator".
func floatParam(r *http.Request, name string) (*float64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, errx.NewValidationError("invalid field: "+name, errx.Field{
			Field: name, Rule: "numeric", Message: "must be a number",
		})
	}
	return &v, nil
}

// timeParam reads a required RFC 3339 timestamp query parameter — the shape a route uses
// when the value has to be passed straight back into a point lookup (a hypertable's
// primary key includes its partitioning column, so an id alone is not enough).
func timeParam(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, errx.NewValidationError("missing field: "+name, errx.Field{
			Field: name, Rule: "required", Message: "is required",
		})
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, errx.NewValidationError("invalid field: "+name, errx.Field{
			Field: name, Rule: "datetime", Message: "must be an RFC 3339 timestamp",
		})
	}
	return t, nil
}

// optionalTimeParam is timeParam for a bound a caller may leave to the service's default: absent is
// nil rather than the zero time, which a service could not tell from "1 January year one".
func optionalTimeParam(r *http.Request, name string) (*time.Time, error) {
	if r.URL.Query().Get(name) == "" {
		return nil, nil
	}
	t, err := timeParam(r, name)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// optionalIDParam reads an opaque id from the query string, or the zero id when it is absent.
// The zero is what every filter already reads as "not narrowed", so absent and unset stay one
// thing; a malformed id is still a 400 rather than a silently unfiltered list.
func optionalIDParam[K id.Kind](r *http.Request, name string) (id.ID[K], error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	return id.Parse[K](raw)
}

// splitList reads a comma-separated query parameter — the `style: form, explode: false` shape
// the spec uses for every list of ids. An empty value is no items rather than one empty one.
func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// decodeBody reads the JSON body. It does not validate: the fields that come from the
// token and the path are filled in after this and before check, so validating here would
// reject a request for a missing actor the handler was about to supply.
func decodeBody(r *http.Request, dst any) error {
	if err := httpx.DecodeJSON(r, dst); err != nil {
		return errx.ErrBadRequestBody
	}
	return nil
}

// decodeOptionalBody is for the bodies the spec marks `required: false` — a logout that
// may name a device, a mark-read that may carry a bound. No body at all is a valid
// request, so only a malformed one fails.
func decodeOptionalBody(r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return decodeBody(r, dst)
}

// check validates the assembled request — body, path and token together — and turns the
// validator's result into the API's per-field error.
func check(v structValidator, req any) error {
	return validation.AsError(v.Struct(req))
}

// structValidator is the subset of *validator.Validate these helpers need, named so they do
// not drag the concrete type into every signature.
type structValidator interface {
	Struct(any) error
}

// cursorMeta converts a service's next-page cursor into the envelope's. It takes the string
// rather than each module's own CursorInfo, which is what let three identical copies of this
// exist — NextCursor is a pointer here so the last page says null instead of omitting the key.
func cursorMeta(next string) httpx.CursorMeta {
	if next == "" {
		return httpx.CursorMeta{}
	}
	return httpx.CursorMeta{NextCursor: new(next)}
}
