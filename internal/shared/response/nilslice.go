package response

import (
	"encoding/json"
	"reflect"
)

// jsonMarshalerType is used to skip recursing into types that define their
// own JSON representation (e.g. time.Time). Without this guard, the
// recursive field copy in replaceNilSlicesRecursive silently zeroes such
// types because their fields are unexported and CanInterface() is false.
var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

func MarshalJSONWithEmptyArrays(v any) ([]byte, error) {
	processed := replaceNilSlices(v)
	return json.Marshal(processed)
}

func replaceNilSlices(data any) any {
	if data == nil {
		return data
	}

	val := reflect.ValueOf(data)
	return replaceNilSlicesRecursive(val).Interface()
}

func replaceNilSlicesRecursive(val reflect.Value) reflect.Value {
	// Dereference pointers
	for val.Kind() == reflect.Ptr {
		if val.IsNil() {
			// Return nil pointer of same type
			return reflect.New(val.Type()).Elem()
		}
		val = val.Elem()
	}

	// Types with a custom JSON representation (json.RawMessage, time.Time, …)
	// own their wire form and must be returned untouched. Walking them
	// corrupts the value — e.g. json.RawMessage is a named []byte, so the
	// slice branch would rewrite a nil RawMessage into an empty []byte,
	// which marshals to "" → "unexpected end of JSON input".
	if val.Type().Implements(jsonMarshalerType) ||
		reflect.PointerTo(val.Type()).Implements(jsonMarshalerType) {
		return val
	}

	switch val.Kind() {
	case reflect.Slice:
		if val.IsNil() {
			// Return empty slice of same type
			return reflect.MakeSlice(val.Type(), 0, 0)
		}

		// Process slice elements
		result := reflect.MakeSlice(val.Type(), val.Len(), val.Len())
		for i := 0; i < val.Len(); i++ {
			elem := replaceNilSlicesRecursive(val.Index(i))
			if elem.IsValid() && elem.CanInterface() {
				// Create a new value that can be assigned
				elemValue := reflect.New(val.Type().Elem()).Elem()
				if elemValue.CanSet() {
					// Try to set the value, handling type conversion
					setValue(elemValue, elem)
					result.Index(i).Set(elemValue)
				}
			}
		}
		return result

	case reflect.Map:
		if val.IsNil() {
			// Return empty map
			return reflect.MakeMap(val.Type())
		}

		result := reflect.MakeMap(val.Type())
		iter := val.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := replaceNilSlicesRecursive(iter.Value())
			if value.IsValid() {
				result.SetMapIndex(key, value)
			}
		}
		return result

	case reflect.Struct:
		result := reflect.New(val.Type()).Elem()
		for i := 0; i < val.NumField(); i++ {
			fieldValue := val.Field(i)
			if !fieldValue.CanInterface() {
				continue
			}

			processed := replaceNilSlicesRecursive(fieldValue)
			if processed.IsValid() && result.Field(i).CanSet() {
				setField(result.Field(i), processed)
			}
		}
		return result

	case reflect.Interface:
		if val.IsNil() {
			return reflect.ValueOf([]any{})
		}
		return replaceNilSlicesRecursive(val.Elem())

	default:
		return val
	}
}

// Helper function to set values safely.
func setValue(dest, src reflect.Value) {
	if !dest.IsValid() || !src.IsValid() {
		return
	}

	destType := dest.Type()
	srcType := src.Type()

	// If types match directly
	if srcType.AssignableTo(destType) {
		dest.Set(src)
		return
	}

	// If src can be converted to dest type
	if srcType.ConvertibleTo(destType) {
		dest.Set(src.Convert(destType))
		return
	}

	// For interface{} destination
	if destType.Kind() == reflect.Interface {
		dest.Set(src)
		return
	}

	// If src is nil and dest is a slice
	if src.Kind() == reflect.Ptr && src.IsNil() && destType.Kind() == reflect.Slice {
		dest.Set(reflect.MakeSlice(destType, 0, 0))
		return
	}
}

// Helper function to set struct fields.
func setField(field reflect.Value, value reflect.Value) {
	if !field.CanSet() || !value.IsValid() {
		return
	}

	fieldType := field.Type()
	valueType := value.Type()

	// Direct assignment
	if valueType.AssignableTo(fieldType) {
		field.Set(value)
		return
	}

	// Type conversion
	if valueType.ConvertibleTo(fieldType) {
		field.Set(value.Convert(fieldType))
		return
	}

	// For pointer to struct field
	if fieldType.Kind() == reflect.Ptr && valueType.AssignableTo(fieldType.Elem()) {
		ptr := reflect.New(fieldType.Elem())
		ptr.Elem().Set(value)
		field.Set(ptr)
		return
	}

	// For slice field with nil value
	if fieldType.Kind() == reflect.Slice && (value.Kind() == reflect.Ptr && value.IsNil()) {
		field.Set(reflect.MakeSlice(fieldType, 0, 0))
		return
	}
}
