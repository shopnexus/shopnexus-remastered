package binder

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// Custom binder that extends Echo's default binder
type CustomBinder struct {
	*echo.DefaultBinder
}

func NewCustomBinder() *CustomBinder {
	return &CustomBinder{
		DefaultBinder: &echo.DefaultBinder{},
	}
}

func (cb *CustomBinder) Bind(i interface{}, c echo.Context) error {
	// First, identify and handle comma-separated fields
	commaSeparatedFields := cb.getCommaSeparatedFieldMap(i)

	// Handle comma-separated fields first
	if err := cb.bindCommaSeparatedFields(i, c, commaSeparatedFields); err != nil {
		return err
	}

	// Then handle regular fields with modified query params
	return cb.bindRegularFields(i, c, commaSeparatedFields)
}

func (cb *CustomBinder) getCommaSeparatedFieldMap(i interface{}) map[string]bool {
	commaSeparatedFields := make(map[string]bool)
	rt := reflect.TypeOf(i)

	// Handle pointer to struct
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.Tag.Get("comma_separated") == "true" {
			if queryTag := field.Tag.Get("query"); queryTag != "" {
				commaSeparatedFields[queryTag] = true
			}
		}
	}

	return commaSeparatedFields
}

func (cb *CustomBinder) bindCommaSeparatedFields(i interface{}, c echo.Context, commaSeparatedFields map[string]bool) error {
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

		param := values.Get(queryTag)
		if param == "" {
			continue
		}

		// Handle both slice and non-slice fields
		if field.Kind() == reflect.Slice {
			if err := cb.setSliceFromCommaSeparated(field, param); err != nil {
				return fmt.Errorf("error parsing %s: %w", queryTag, err)
			}
		} else {
			// For non-slice fields, take the first value
			parts := strings.Split(param, ",")
			if len(parts) > 0 {
				if err := cb.setSingleValueFromString(field, strings.TrimSpace(parts[0])); err != nil {
					return fmt.Errorf("error parsing %s: %w", queryTag, err)
				}
			}
		}
	}

	return nil
}

func (cb *CustomBinder) bindRegularFields(i interface{}, c echo.Context, commaSeparatedFields map[string]bool) error {
	// Create a new request with comma-separated fields removed
	originalReq := c.Request()
	values := originalReq.URL.Query()

	// Filter out comma-separated fields
	filteredValues := make(map[string][]string)
	for key, vals := range values {
		if !commaSeparatedFields[key] {
			filteredValues[key] = vals
		}
	}

	// Build new query string
	var queryParts []string
	for key, vals := range filteredValues {
		for _, val := range vals {
			queryParts = append(queryParts, key+"="+val)
		}
	}

	// Temporarily modify the request URL
	originalRawQuery := originalReq.URL.RawQuery
	originalReq.URL.RawQuery = strings.Join(queryParts, "&")

	// Use default binder for remaining fields
	err := cb.DefaultBinder.Bind(i, c)

	// Restore original query
	originalReq.URL.RawQuery = originalRawQuery

	return err
}

func (cb *CustomBinder) setSliceFromCommaSeparated(field reflect.Value, param string) error {
	parts := strings.Split(param, ",")
	elemType := field.Type().Elem()

	slice := reflect.MakeSlice(field.Type(), 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

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
