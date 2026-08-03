package domain_test

import (
	"errors"
	"testing"
	"time"

	"shopnexus/internal/module/account/domain"
)

func TestNewNotification(t *testing.T) {
	tests := []struct {
		name   string
		params domain.NewNotificationParams
		want   error
	}{
		{
			name: "valid",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.CategoryOrder,
				Title:     "Your order shipped",
				Payload:   map[string]any{"order_id": "ord_x"},
			},
		},
		{
			name: "account required",
			params: domain.NewNotificationParams{
				Category: domain.CategoryOrder,
				Title:    "t",
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "title required",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.CategoryOrder,
			},
			want: domain.ErrNotificationInvalid,
		},
		{
			name: "category must be known",
			params: domain.NewNotificationParams{
				AccountID: 42,
				Category:  domain.Category("gossip"),
				Title:     "t",
			},
			want: domain.ErrNotificationInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewNotification(tt.params)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want != nil {
				return
			}
			if got.AccountID != tt.params.AccountID {
				t.Errorf("AccountID = %d, want %d", got.AccountID, tt.params.AccountID)
			}
			if got.CreatedAt.IsZero() {
				t.Error("CreatedAt is zero; the constructor stamps it")
			}
			if got.ReadAt != nil {
				t.Error("ReadAt should be nil on a fresh notification")
			}
		})
	}
}

// A scheduled notification is not yet delivered, so it must not read as unread now.
func TestNewNotificationScheduled(t *testing.T) {
	at := time.Now().Add(time.Hour)
	got, err := domain.NewNotification(domain.NewNotificationParams{
		AccountID:   7,
		Category:    domain.CategorySystem,
		Title:       "Maintenance",
		ScheduledAt: &at,
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	if got.ScheduledAt == nil || !got.ScheduledAt.Equal(at) {
		t.Fatalf("ScheduledAt = %v, want %v", got.ScheduledAt, at)
	}
}
