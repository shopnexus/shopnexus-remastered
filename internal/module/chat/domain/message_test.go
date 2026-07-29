package domain_test

import (
	"testing"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/shared/errx"
)

func TestNewMessage_Valid(t *testing.T) {
	m, err := domain.NewMessage(3, 7, "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Body != "Hello" {
		t.Fatalf("body = %q, want Hello", m.Body)
	}
}

func TestNewMessage_EmptyBody(t *testing.T) {
	_, err := domain.NewMessage(3, 7, "")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestNewMessage_ZeroSender(t *testing.T) {
	_, err := domain.NewMessage(3, 0, "Hello")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestNewMessage_ZeroConversation(t *testing.T) {
	_, err := domain.NewMessage(0, 7, "Hello")
	if status, _, _, ok := errx.Decompose(err); !ok || status != 400 {
		t.Fatalf("expected Invalid, got %v", err)
	}
}
