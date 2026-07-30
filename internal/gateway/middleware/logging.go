package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"shopnexus/internal/gateway/gwctx"
	"shopnexus/internal/shared/httpx"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap and Flush keep streaming working through this wrapper — see the same pair on the
// observability recorder. This one matters twice over: Logging is the outermost
// middleware, so without it nothing inside can flush no matter what the layers below do.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := strconv.FormatInt(start.UnixNano(), 36)
			ctx := gwctx.WithRequestID(r.Context(), reqID)

			// Set before the handler runs, so it is on every response whatever happens —
			// a 204, a stream, a panic recovered upstream — and so httpx.WriteError can
			// read it back off the writer instead of shared/ having to import gwctx,
			// which would point a dependency from shared at the gateway.
			w.Header().Set(httpx.RequestIDHeader, reqID)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			log.Info("request",
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"latency_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
