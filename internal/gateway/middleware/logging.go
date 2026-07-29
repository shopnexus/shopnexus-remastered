package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"shopnexus/internal/gateway/gwctx"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := strconv.FormatInt(start.UnixNano(), 36)
			ctx := gwctx.WithRequestID(r.Context(), reqID)

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
