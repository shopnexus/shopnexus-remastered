package account

import (
	"context"
	"fmt"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// RegisterDevice is an upsert on the push token: the same install signing in as another
// user moves the row instead of creating a second one, so the previous owner stops
// receiving that phone's notifications.
func (s *Service) RegisterDevice(ctx context.Context, req accountapi.RegisterDeviceRequest) (accountapi.Device, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Device{}, err
	}
	d, err := domain.NewDevice(req.ActorID.Int64(), domain.Platform(req.Platform), req.PushToken)
	if err != nil {
		return accountapi.Device{}, err
	}
	if err := s.repo.UpsertDevice(ctx, &d); err != nil {
		return accountapi.Device{}, fmt.Errorf("upsert device: %w", err)
	}
	return toAPIDevice(d), nil
}

func (s *Service) ListDevices(ctx context.Context, req accountapi.ListDevicesRequest) ([]accountapi.Device, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListDevices(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	out := make([]accountapi.Device, 0, len(rows))
	for _, d := range rows {
		out = append(out, toAPIDevice(d))
	}
	return out, nil
}

func (s *Service) DeleteDevice(ctx context.Context, req accountapi.DeleteDeviceRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	d, err := s.repo.FindDevice(ctx, req.ID.Int64())
	if err != nil {
		return fmt.Errorf("find device: %w", err)
	}
	if !d.Owns(req.ActorID.Int64()) {
		return domain.ErrForbidden
	}
	if err := s.repo.DeleteDevice(ctx, d.ID); err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}

func toAPIDevice(d domain.Device) accountapi.Device {
	return accountapi.Device{
		ID:              id.Of[id.Device](d.ID),
		Platform:        string(d.Platform),
		PushTokenSuffix: d.TokenSuffix(),
		LastSeenAt:      d.LastSeenAt,
		CreatedAt:       d.CreatedAt,
	}
}
