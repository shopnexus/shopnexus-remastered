package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo, // fallback
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNew_NotNil(t *testing.T) {
	if New(Options{Level: "debug"}) == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNew_JSONWithServiceAttr(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: "info", Service: "gateway", Writer: &buf})
	log.Info("hello", "k", "v")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, buf.String())
	}
	if rec["service"] != "gateway" {
		t.Errorf("service = %v, want gateway", rec["service"])
	}
	if rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("unexpected record: %v", rec)
	}
}

func TestNew_LevelFilters(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: "warn", Writer: &buf})
	log.Info("dropped")
	if buf.Len() != 0 {
		t.Errorf("info should be filtered at warn level, got %q", buf.String())
	}
}
