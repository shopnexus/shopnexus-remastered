package observability

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopnexus/internal/infra/eventbus"
)

// Reading a response's status must not cost the handler its ability to stream. The
// recorder wraps the writer, so without Unwrap and Flush an SSE or upgrade handler
// silently buffers behind the middleware that only wanted the status code.
func TestMiddleware_PreservesFlush(t *testing.T) {
	bus := eventbus.NewMemory(slog.Default())
	t.Cleanup(func() { _ = bus.Close() })

	var viaController, viaAssertion bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaController = http.NewResponseController(w).Flush() == nil
		_, viaAssertion = w.(http.Flusher)
	})

	sink := NewSink(bus, slog.Default(), "test")
	sink.Middleware(h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if !viaController {
		t.Error("http.ResponseController could not flush through the recorder")
	}
	if !viaAssertion {
		t.Error("handler could not assert http.Flusher through the recorder")
	}
}
