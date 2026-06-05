package binder

import (
	"encoding"
	stderrors "errors"
	"fmt"
	"reflect"
	"shopnexus-server/internal/shared/errors"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// CustomBinder that extends Echo's default binder.
type CustomBinder struct {
	*echo.DefaultBinder
}

func NewCustomBinder() *CustomBinder {
	return &CustomBinder{
		DefaultBinder: &echo.DefaultBinder{},
	}
}

func (cb *CustomBinder) Bind(i any, c echo.Context) error {
	// First, identify and handle comma-separated fields
	commaSeparatedFields := cb.getCommaSeparatedFieldMap(i)

	// Handle comma-separated fields first
	if err := cb.bindCommaSeparatedFields(i, c, commaSeparatedFields); err != nil {
		return errors.ErrValidation.Fmt("failed to bind comma-separated fields: %v", err)
	}

	// Then handle regular fields with modified query params
	if err := cb.bindRegularFields(i, c, commaSeparatedFields); err != nil {
		return errors.ErrValidation.Fmt(err.Error())
	}

	return nil
}

func (cb *CustomBinder) getCommaSeparatedFieldMap(i any) map[string]bool {
	commaSeparatedFields := make(map[string]bool)
	rt := reflect.TypeOf(i)

	// Handle pointer to struct
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}

	for field := range rt.Fields() {
		field := field
		if field.Tag.Get("comma_separated") == "true" {
			if queryTag := field.Tag.Get("query"); queryTag != "" {
				commaSeparatedFields[queryTag] = true
			}
		}
	}

	return commaSeparatedFields
}

func (cb *CustomBinder) bindCommaSeparatedFields(i any, c echo.Context, commaSeparatedFields map[string]bool) error {
	values := c.Request().URL.Query()
	rv := reflect.ValueOf(i)

	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	rt := rv.Type()

	for j := 0; j < rv.NumField(); j++ {
		field := rv.Field(j)
		fieldType := rt.Field(j)

		if !field.CanSet() {
			continue
		}

		queryTag := fieldType.Tag.Get("query")
		if queryTag == "" || !commaSeparatedFields[queryTag] {
			continue
		}

		params := values[queryTag]
		if len(params) == 0 {
			continue
		}

		// Handle both slice and non-slice fields
		if field.Kind() == reflect.Slice {
			// Support both repeated params (?k=a&k=b) and comma-separated (?k=a,b)
			var allParts []string
			for _, p := range params {
				for part := range strings.SplitSeq(p, ",") {
					part = strings.TrimSpace(part)
					if part != "" {
						allParts = append(allParts, part)
					}
				}
			}
			if err := cb.setSliceFromParts(field, allParts); err != nil {
				return fmt.Errorf("error parsing %s: %w", queryTag, err)
			}
		} else {
			// For non-slice fields, take the first value
			parts := strings.Split(params[0], ",")
			if len(parts) > 0 {
				if err := cb.setSingleValueFromString(field, strings.TrimSpace(parts[0])); err != nil {
					return fmt.Errorf("error parsing %s: %w", queryTag, err)
				}
			}
		}
	}

	return nil
}

func (cb *CustomBinder) bindRegularFields(i any, c echo.Context, commaSeparatedFields map[string]bool) error {
	rv := reflect.ValueOf(i)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	// Bind query and path params field by field with descriptive error messages
	if err := cb.bindStructFields(rv, rv.Type(), c, commaSeparatedFields); err != nil {
		return err
	}

	// Bind body using default binder (JSON errors already include field context)
	if err := cb.DefaultBinder.BindBody(c, i); err != nil {
		he := &echo.HTTPError{}
		if stderrors.As(err, &he) {
			return errors.ErrValidation.Fmt(he.Internal.Error())
		}
		return errors.ErrValidation.Fmt(err.Error())
	}

	return nil
}

func (cb *CustomBinder) bindStructFields(
	rv reflect.Value,
	rt reflect.Type,
	c echo.Context,
	commaSeparatedFields map[string]bool,
) error {
	for j := 0; j < rt.NumField(); j++ {
		field := rv.Field(j)
		fieldType := rt.Field(j)

		// Recurse into embedded structs
		if fieldType.Anonymous {
			embedded := field
			embeddedType := fieldType.Type
			if embedded.Kind() == reflect.Ptr {
				if embedded.IsNil() {
					embedded.Set(reflect.New(embeddedType.Elem()))
				}
				embedded = embedded.Elem()
				embeddedType = embeddedType.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				if err := cb.bindStructFields(embedded, embeddedType, c, commaSeparatedFields); err != nil {
					return err
				}
			}
			continue
		}

		if !field.CanSet() {
			continue
		}

		// Bind query params
		if queryTag := fieldType.Tag.Get("query"); queryTag != "" {
			if commaSeparatedFields[queryTag] {
				continue
			}
			// For slice fields, bind all repeated query values (?key=a&key=b)
			if field.Kind() == reflect.Slice {
				if parts := c.QueryParams()[queryTag]; len(parts) > 0 {
					if err := cb.setSliceFromParts(field, parts); err != nil {
						return errors.ErrValidation.Fmt("query param '%s': %v", queryTag, err)
					}
				}
				continue
			}
			if value := c.QueryParam(queryTag); value != "" {
				if err := cb.setSingleValueFromString(field, value); err != nil {
					return errors.ErrValidation.Fmt("query param '%s': %v", queryTag, err)
				}
			}
			continue
		}

		// Bind path params
		if paramTag := fieldType.Tag.Get("param"); paramTag != "" {
			if value := c.Param(paramTag); value != "" {
				if err := cb.setSingleValueFromString(field, value); err != nil {
					return errors.ErrValidation.Fmt("path param '%s': %v", paramTag, err)
				}
			}
			continue
		}
	}
	return nil
}

func (cb *CustomBinder) setSliceFromParts(field reflect.Value, parts []string) error {
	elemType := field.Type().Elem()

	slice := reflect.MakeSlice(field.Type(), 0, len(parts))

	for _, part := range parts {
		elem := reflect.New(elemType).Elem()

		if err := cb.setSingleValueFromString(elem, part); err != nil {
			return err
		}

		slice = reflect.Append(slice, elem)
	}

	field.Set(slice)
	return nil
}

func (cb *CustomBinder) setSingleValueFromString(field reflect.Value, value string) error {
	// Support types implementing encoding.TextUnmarshaler (e.g., uuid.UUID, uuid.NullUUID, null.String)
	if field.CanAddr() {
		if tu, ok := field.Addr().Interface().(encoding.TextUnmarshaler); ok {
			return tu.UnmarshalText([]byte(value))
		}
	}
	if tu, ok := field.Interface().(encoding.TextUnmarshaler); ok {
		return tu.UnmarshalText([]byte(value))
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		val, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer value: %s", value)
		}
		field.SetInt(val)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		val, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid unsigned integer value: %s", value)
		}
		field.SetUint(val)

	case reflect.Bool:
		val, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid bool value: %s", value)
		}
		field.SetBool(val)

	case reflect.Float32, reflect.Float64:
		val, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid float value: %s", value)
		}
		field.SetFloat(val)

	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	return nil
}
