package commonbiz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	commondb "shopnexus-server/internal/module/common/db/sqlc"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/validator"
)

type ListOptionParams struct {
	Type      []string `validate:"required"`
	IsEnabled []bool   `validate:"omitempty,dive"`
	// AccountID identifies the requester. Zero (uuid.NullUUID{}) for anonymous callers;
	// when valid, items whose OwnerID matches are flagged Owned=true in the response.
	AccountID uuid.NullUUID
}

// OptionListItem is the response shape for ListOption.
// OwnerID is intentionally hidden; ownership is exposed via the Owned flag.
type OptionListItem struct {
	ID          string                 `json:"id"`
	Type        sharedmodel.OptionType `json:"type"`
	Provider    string                 `json:"provider"`
	IsEnabled   bool                   `json:"is_enabled"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Priority    int32                  `json:"priority"`
	LogoRsID    uuid.NullUUID          `json:"logo_rs_id"`
	Data        json.RawMessage        `json:"data"`
	Owned       bool                   `json:"owned"`
}

type UpsertOptionsParams struct {
	Type    string               `json:"type" validate:"required"`
	Configs []sharedmodel.Option `json:"configs"  validate:"required"`
}

type DeleteOptionParams struct {
	IDs []string `json:"ids" validate:"required,min=1"`
}

// UpsertOptions persists a batch of service options (insert or update by ID).
func (b *CommonHandler) UpsertOptions(ctx context.Context, params UpsertOptionsParams) error {
	return b.upsertOptions(ctx, params)
}

// upsertOptions is the context-agnostic implementation, used at init time
// where we hold a plain context.Context (not a Restate one).
func (b *CommonHandler) upsertOptions(ctx context.Context, params UpsertOptionsParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate upsert options: %w", err)
	}

	q := b.storage.Querier()
	for _, cfg := range params.Configs {
		data := cfg.Data
		if len(data) == 0 {
			data = []byte("{}")
		}
		if err := q.UpsertOption(ctx, commondb.UpsertOptionParams{
			ID:          cfg.ID,
			OwnerID:     cfg.OwnerID,
			IsEnabled:   true,
			Name:        cfg.Name,
			Description: cfg.Description,
			Priority:    cfg.Priority,
			LogoRsID:    cfg.LogoRsID,
			Data:        data,
			Type:        params.Type,
			Provider:    cfg.Provider,
		}); err != nil {
			return fmt.Errorf("db upsert option: %w", err)
		}
	}
	return nil
}

// DeleteOptions deletes options by ID. Idempotent — missing IDs are silently
// ignored at the SQL layer (DELETE … WHERE id = ANY(...)).
func (b *CommonHandler) DeleteOptions(ctx context.Context, params DeleteOptionParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate delete options: %w", err)
	}
	if err := b.storage.Querier().DeleteOption(ctx, commondb.DeleteOptionParams{
		ID: params.IDs,
	}); err != nil {
		return fmt.Errorf("db delete option: %w", err)
	}
	return nil
}

// ListOption returns active service options filtered by category.
// Each item is tagged Owned=true when its OwnerID matches params.AccountID.
func (b *CommonHandler) ListOption(
	ctx context.Context,
	params ListOptionParams,
) ([]OptionListItem, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list service option: %w", err)
	}

	dbOptions, err := b.storage.Querier().ListSortedOption(ctx, commondb.ListSortedOptionParams{
		Type:      params.Type,
		IsEnabled: params.IsEnabled,
		AccountID: params.AccountID,
	})
	if err != nil {
		return nil, fmt.Errorf("db list service option: %w", err)
	}

	var result []OptionListItem
	for _, opts := range dbOptions {
		owned := params.AccountID.Valid && opts.OwnerID.Valid && opts.OwnerID.UUID == params.AccountID.UUID
		result = append(result, OptionListItem{
			ID:          opts.ID,
			Type:        sharedmodel.OptionType(opts.Type),
			Provider:    opts.Provider,
			IsEnabled:   opts.IsEnabled,
			Name:        opts.Name,
			Description: opts.Description,
			Priority:    opts.Priority,
			LogoRsID:    opts.LogoRsID,
			Data:        opts.Data,
			Owned:       owned,
		})
	}

	return result, nil
}
