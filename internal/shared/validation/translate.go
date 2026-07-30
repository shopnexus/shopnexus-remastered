package validation

import (
	"errors"
	"strings"

	"github.com/go-playground/validator/v10"

	"shopnexus/internal/shared/errx"
)

// AsError turns whatever the validator returned into the API's validation error, with one
// entry per field that failed.
//
// It exists because validator reports a rich result — a field path, the rule that
// rejected it, the rule's parameter — and the wire shape used to keep none of it: every
// failure collapsed into one sentence, so a client could tell that the request was
// invalid but not which input to mark. Anything that is not a ValidationErrors (a bad
// tag, a nil pointer) is a programming mistake rather than a bad request, so it is
// returned unchanged and becomes a 500.
func AsError(err error) error {
	if err == nil {
		return nil
	}
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return err
	}

	fields := make([]errx.Field, 0, len(ve))
	for _, fe := range ve {
		fields = append(fields, errx.Field{
			Field:   path(fe),
			Rule:    fe.Tag(),
			Message: message(fe),
		})
	}
	return errx.NewValidationError(summary(fields), fields...)
}

// path is the field's location in the request body, dotted from the root and with the
// top-level struct name dropped — a client sent a body, not a Go type, and
// "CreateListingRequest.skus[0].price" names something it never saw. Namespace is what
// carries the index of a repeated field, which is the whole reason a form can point at
// the right row.
//
// The first segment is only dropped when it is a bare name: an anonymous root struct has
// no name in the namespace at all, so "skus[0].price" starts with a real field, and
// cutting at the first dot would report "price" and lose which row it was in.
func path(fe validator.FieldError) string {
	ns := fe.Namespace()
	i := strings.IndexByte(ns, '.')
	if i < 0 {
		return ns
	}
	if strings.ContainsAny(ns[:i], "[]") {
		return ns
	}
	return ns[i+1:]
}

// message is deliberately plain and in English: it is for a developer reading a log or a
// response by hand. A user-facing string is the client's to produce, from "rule" and the
// field it belongs to, in the user's own language.
func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "min":
		return "must be at least " + fe.Param()
	case "max":
		return "must be at most " + fe.Param()
	case "gt":
		return "must be greater than " + fe.Param()
	case "gte":
		return "must be at least " + fe.Param()
	case "lt":
		return "must be less than " + fe.Param()
	case "lte":
		return "must be at most " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	case "len":
		return "must be exactly " + fe.Param() + " long"
	default:
		if fe.Param() != "" {
			return "failed the " + fe.Tag() + " rule (" + fe.Param() + ")"
		}
		return "failed the " + fe.Tag() + " rule"
	}
}

// summary names the offending fields rather than repeating their messages: the detail is
// already in "fields", and a log line wants to be one line.
func summary(fields []errx.Field) string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Field)
	}
	if len(names) == 1 {
		return "invalid field: " + names[0]
	}
	return "invalid fields: " + strings.Join(names, ", ")
}
