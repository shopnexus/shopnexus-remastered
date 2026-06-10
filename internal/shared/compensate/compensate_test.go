package compensate_test

import (
	"context"
	"errors"
	"testing"

	"shopnexus-server/internal/shared/compensate"
)

func TestCompensateRunsLIFO(t *testing.T) {
	var order []int
	s := compensate.New(context.Background())
	s.Defer("first", func(context.Context) error { order = append(order, 1); return nil })
	s.Defer("second", func(context.Context) error { order = append(order, 2); return nil })

	if err := s.Compensate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Fatalf("want LIFO [2 1], got %v", order)
	}
}

func TestClearSkipsCompensation(t *testing.T) {
	ran := false
	s := compensate.New(context.Background())
	s.Defer("x", func(context.Context) error { ran = true; return nil })
	s.Clear()
	if err := s.Compensate(); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("cleared compensator should not run")
	}
}

func TestCompensateStopsOnError(t *testing.T) {
	var ran []string
	boom := errors.New("boom")
	s := compensate.New(context.Background())
	s.Defer("bottom", func(context.Context) error { ran = append(ran, "bottom"); return nil })
	s.Defer("top", func(context.Context) error { ran = append(ran, "top"); return boom })

	err := s.Compensate()
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if len(ran) != 1 || ran[0] != "top" {
		t.Fatalf("want stop after top, got %v", ran)
	}
}
