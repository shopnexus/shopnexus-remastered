// Package logger builds the application's structured JSON *slog.Logger.
// JSON on stdout is the contract for log shipping: a collector (Grafana Alloy)
// tails the container's stdout into Loki, and the `service` attribute lets
// Grafana filter one service's logs among many.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Options configures the logger. Level is required (debug|info|warn|error);
// Service tags every line; Source adds caller file:line; Writer defaults to
// stdout (override in tests).
type Options struct {
	Level   string
	Service string
	Source  bool
	Writer  io.Writer
}

// New returns a JSON logger. Every record carries the `service` attribute when
// Service is set, so logs stay filterable once the app is split across services.
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}
	log := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     parseLevel(opts.Level),
		AddSource: opts.Source,
	}))
	if opts.Service != "" {
		log = log.With("service", opts.Service)
	}
	return log
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
