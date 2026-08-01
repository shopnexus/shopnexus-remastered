package validation_test

import (
	"testing"

	"shopnexus/internal/shared/validation"
)

func TestDefault_RequiredTagFails(t *testing.T) {
	v := validation.Default()
	s := struct {
		Name string `validate:"required"`
	}{}
	if err := v.Struct(s); err == nil {
		t.Fatal("expected validation error for empty required field, got nil")
	}
}

func TestDefault_ValidPasses(t *testing.T) {
	v := validation.Default()
	s := struct {
		Name string `validate:"required"`
	}{Name: "ok"}
	if err := v.Struct(s); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDefault_ReturnsSameInstance(t *testing.T) {
	first, second := validation.Default(), validation.Default()
	if first != second {
		t.Fatal("Default() must return the same singleton instance")
	}
}
