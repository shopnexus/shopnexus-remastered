package nullutil_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"github.com/labstack/echo/v4"

	"shopnexus-server/internal/shared/binder"
	"shopnexus-server/internal/shared/validator"
)

// testStructPtr uses Go pointers for nullable fields.
type testStructPtr struct {
	Name   *string    `json:"name"   validate:"omitnil"`
	Price  *float64   `json:"price"  validate:"omitnil"`
	Count  *int64     `json:"count"  validate:"omitnil"`
	Active *bool      `json:"active" validate:"omitnil"`
	Since  *time.Time `json:"since"  validate:"omitnil"`
}

// testStructNull uses guregu/null types for nullable fields.
type testStructNull struct {
	Name   null.String `json:"name"   validate:"omitnil"`
	Price  null.Float  `json:"price"  validate:"omitnil"`
	Count  null.Int64  `json:"count"  validate:"omitnil"`
	Active null.Bool   `json:"active" validate:"omitnil"`
	Since  null.Time   `json:"since"  validate:"omitnil"`
}

var (
	strVal  = "gaming-keyboard"
	fltVal  = 29.99
	intVal  = int64(42)
	boolVal = true
	timeVal = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	jsonWithValue []byte
	jsonNull      = []byte(`{"name":null,"price":null,"count":null,"active":null,"since":null}`)
	jsonEmpty     = []byte("{}")
)

func init() {
	s := makePtr(true)
	jsonWithValue, _ = json.Marshal(s)
}

func makePtr(val bool) testStructPtr {
	if !val {
		return testStructPtr{}
	}
	return testStructPtr{
		Name: &strVal, Price: &fltVal, Count: &intVal,
		Active: &boolVal, Since: &timeVal,
	}
}

func makeNull(val bool) testStructNull {
	if !val {
		return testStructNull{}
	}
	return testStructNull{
		Name: null.StringFrom(strVal), Price: null.FloatFrom(fltVal),
		Count: null.IntFrom(intVal), Active: null.BoolFrom(boolVal),
		Since: null.TimeFrom(timeVal),
	}
}

// ---- JSON marshal ----

func BenchmarkJSONMarshal(b *testing.B) {
	b.Run("Ptr/WithValue", benchJSONMarshalPtr(true))
	b.Run("Ptr/Empty", benchJSONMarshalPtr(false))
	b.Run("Null/WithValue", benchJSONMarshalNull(true))
	b.Run("Null/Empty", benchJSONMarshalNull(false))
}

func benchJSONMarshalPtr(val bool) func(b *testing.B) {
	return func(b *testing.B) {
		s := makePtr(val)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			json.Marshal(s)
		}
	}
}

func benchJSONMarshalNull(val bool) func(b *testing.B) {
	return func(b *testing.B) {
		s := makeNull(val)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			json.Marshal(s)
		}
	}
}

// ---- SONIC marshal ----

func BenchmarkSONICMarshal(b *testing.B) {
	b.Run("Ptr/WithValue", benchSONICMarshalPtr(true))
	b.Run("Ptr/Empty", benchSONICMarshalPtr(false))
	b.Run("Null/WithValue", benchSONICMarshalNull(true))
	b.Run("Null/Empty", benchSONICMarshalNull(false))
}

func benchSONICMarshalPtr(val bool) func(b *testing.B) {
	return func(b *testing.B) {
		s := makePtr(val)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			json.Marshal(s)
		}
	}
}

func benchSONICMarshalNull(val bool) func(b *testing.B) {
	return func(b *testing.B) {
		s := makeNull(val)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			json.Marshal(s)
		}
	}
}

// ---- JSON unmarshal ----

func BenchmarkJSONUnmarshal(b *testing.B) {
	b.Run("Ptr/WithValue", benchJSONUnmarshalPtr(jsonWithValue))
	b.Run("Ptr/Null", benchJSONUnmarshalPtr(jsonNull))
	b.Run("Ptr/Empty", benchJSONUnmarshalPtr(jsonEmpty))
	b.Run("Null/WithValue", benchJSONUnmarshalNull(jsonWithValue))
	b.Run("Null/Null", benchJSONUnmarshalNull(jsonNull))
	b.Run("Null/Empty", benchJSONUnmarshalNull(jsonEmpty))
}

func benchJSONUnmarshalPtr(data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var s testStructPtr
			json.Unmarshal(data, &s)
		}
	}
}

func benchJSONUnmarshalNull(data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var s testStructNull
			json.Unmarshal(data, &s)
		}
	}
}

// ---- SONIC unmarshal ----

func BenchmarkSONICUnmarshal(b *testing.B) {
	b.Run("Ptr/WithValue", benchSONICUnmarshalPtr(jsonWithValue))
	b.Run("Ptr/Null", benchSONICUnmarshalPtr(jsonNull))
	b.Run("Ptr/Empty", benchSONICUnmarshalPtr(jsonEmpty))
	b.Run("Null/WithValue", benchSONICUnmarshalNull(jsonWithValue))
	b.Run("Null/Null", benchSONICUnmarshalNull(jsonNull))
	b.Run("Null/Empty", benchSONICUnmarshalNull(jsonEmpty))
}

func benchSONICUnmarshalPtr(data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var s testStructPtr
			json.Unmarshal(data, &s)
		}
	}
}

func benchSONICUnmarshalNull(data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var s testStructNull
			json.Unmarshal(data, &s)
		}
	}
}

// ---- Echo bind ----

func BenchmarkEchoBind(b *testing.B) {
	e := echo.New()
	e.Binder = binder.NewCustomBinder()

	b.Run("Ptr/WithValue", benchEchoBindPtr(e, jsonWithValue))
	b.Run("Ptr/Empty", benchEchoBindPtr(e, jsonEmpty))
	b.Run("Null/WithValue", benchEchoBindNull(e, jsonWithValue))
	b.Run("Null/Empty", benchEchoBindNull(e, jsonEmpty))
}

func benchEchoBindPtr(e *echo.Echo, data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			var s testStructPtr
			c.Bind(&s)
		}
	}
}

func benchEchoBindNull(e *echo.Echo, data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			var s testStructNull
			c.Bind(&s)
		}
	}
}

// ---- Echo validate ----

func BenchmarkEchoValidate(b *testing.B) {
	v, err := validator.New()
	if err != nil {
		b.Fatal(err)
	}
	e := echo.New()
	e.Validator = v

	b.Run("Ptr/WithValue", benchEchoValidatePtr(e, true))
	b.Run("Ptr/Empty", benchEchoValidatePtr(e, false))
	b.Run("Null/WithValue", benchEchoValidateNull(e, true))
	b.Run("Null/Empty", benchEchoValidateNull(e, false))
}

func benchEchoValidatePtr(e *echo.Echo, val bool) func(b *testing.B) {
	s := makePtr(val)
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			e.Validator.Validate(&s)
		}
	}
}

func benchEchoValidateNull(e *echo.Echo, val bool) func(b *testing.B) {
	s := makeNull(val)
	return func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			e.Validator.Validate(&s)
		}
	}
}

// ---- Field access ----

func BenchmarkFieldAccess(b *testing.B) {
	b.Run("Ptr/WithValue", benchFieldAccessPtr(true))
	b.Run("Ptr/Empty", benchFieldAccessPtr(false))
	b.Run("Null/WithValue", benchFieldAccessNull(true))
	b.Run("Null/Empty", benchFieldAccessNull(false))
}

func benchFieldAccessPtr(val bool) func(b *testing.B) {
	s := makePtr(val)
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var name string
			var price float64
			var count int64
			var active bool
			var since time.Time

			if s.Name != nil {
				name = *s.Name
			}
			if s.Price != nil {
				price = *s.Price
			}
			if s.Count != nil {
				count = *s.Count
			}
			if s.Active != nil {
				active = *s.Active
			}
			if s.Since != nil {
				since = *s.Since
			}

			runtime.KeepAlive(name)
			runtime.KeepAlive(price)
			runtime.KeepAlive(count)
			runtime.KeepAlive(active)
			runtime.KeepAlive(since)
		}
	}
}

func benchFieldAccessNull(val bool) func(b *testing.B) {
	s := makeNull(val)
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var name string
			var price float64
			var count int64
			var active bool
			var since time.Time

			if s.Name.Valid {
				name = s.Name.String
			}
			if s.Price.Valid {
				price = s.Price.Float64
			}
			if s.Count.Valid {
				count = s.Count.Int64
			}
			if s.Active.Valid {
				active = s.Active.Bool
			}
			if s.Since.Valid {
				since = s.Since.Time
			}

			runtime.KeepAlive(name)
			runtime.KeepAlive(price)
			runtime.KeepAlive(count)
			runtime.KeepAlive(active)
			runtime.KeepAlive(since)
		}
	}
}

// ---- Full pipeline: bind + validate ----

func BenchmarkFullPipeline(b *testing.B) {
	v, err := validator.New()
	if err != nil {
		b.Fatal(err)
	}
	e := echo.New()
	e.Binder = binder.NewCustomBinder()
	e.Validator = v

	b.Run("Ptr/WithValue", benchPipelinePtr(e, jsonWithValue))
	b.Run("Ptr/Empty", benchPipelinePtr(e, jsonEmpty))
	b.Run("Null/WithValue", benchPipelineNull(e, jsonWithValue))
	b.Run("Null/Empty", benchPipelineNull(e, jsonEmpty))
}

func benchPipelinePtr(e *echo.Echo, data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			var s testStructPtr
			c.Bind(&s)
			e.Validator.Validate(&s)
		}
	}
}

func benchPipelineNull(e *echo.Echo, data []byte) func(b *testing.B) {
	return func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			var s testStructNull
			c.Bind(&s)
			e.Validator.Validate(&s)
		}
	}
}
