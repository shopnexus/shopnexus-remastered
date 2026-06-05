package promotionbiz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	accountmodel "shopnexus-server/internal/module/account/model"
	promotiondb "shopnexus-server/internal/module/promotion/db/sqlc"
	promotionmodel "shopnexus-server/internal/module/promotion/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"
)

// --- Get ---

type GetPromotionParams struct {
	ID uuid.UUID `validate:"required"`
}

// GetPromotion returns a promotion by ID, including its refs.
func (s *PromotionHandler) GetPromotion(
	ctx restate.Context,
	params GetPromotionParams,
) (promotionmodel.Promotion, error) {
	var zero promotionmodel.Promotion

	promo, err := s.storage.Querier().GetPromotion(ctx, promotiondb.GetPromotionParams{
		ID: uuid.NullUUID{UUID: params.ID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("db get promotion: %w", err)
	}

	refs, err := s.storage.Querier().ListRef(ctx, promotiondb.ListRefParams{
		PromotionID: []uuid.UUID{promo.ID},
	})
	if err != nil {
		return zero, fmt.Errorf("db list promotion refs: %w", err)
	}

	return mapPromotion(promo, refs), nil
}

// --- List ---

type ListPromotionParams struct {
	paginate.Params

	ID []uuid.UUID `validate:"omitempty,dive,required"`
}

// ListPromotion returns a paginated list of promotions with their refs.
func (s *PromotionHandler) ListPromotion(
	ctx restate.Context,
	params ListPromotionParams,
) (paginate.PaginateResult[promotionmodel.Promotion], error) {
	var zero paginate.PaginateResult[promotionmodel.Promotion]

	rows, err := s.storage.Querier().ListCountPromotion(ctx, promotiondb.ListCountPromotionParams{
		Limit:  params.Limit,
		Offset: params.Offset(),
		ID:     params.ID,
	})
	if err != nil {
		return zero, fmt.Errorf("db list promotions: %w", err)
	}

	promoIDs := lo.Map(rows, func(r promotiondb.ListCountPromotionRow, _ int) uuid.UUID {
		return r.PromotionPromotion.ID
	})

	refs, err := s.storage.Querier().ListRef(ctx, promotiondb.ListRefParams{
		PromotionID: promoIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("db list promotion refs: %w", err)
	}
	refsMap := lo.GroupBy(refs, func(r promotiondb.PromotionRef) uuid.UUID { return r.PromotionID })

	var total null.Int64
	if len(rows) > 0 {
		total.SetValid(rows[0].TotalCount)
	}

	return paginate.PaginateResult[promotionmodel.Promotion]{
		PageParams: params.Params,
		Total:      total,
		Data: lo.Map(rows, func(r promotiondb.ListCountPromotionRow, _ int) promotionmodel.Promotion {
			return mapPromotion(r.PromotionPromotion, refsMap[r.PromotionPromotion.ID])
		}),
	}, nil
}

// --- Create ---

type CreatePromotionParams struct {
	Account accountmodel.AuthenticatedAccount

	Code        string                        `validate:"required,alphanum,min=3,max=50"`
	Type        promotionmodel.Type           `validate:"required,validateFn=Valid"`
	Title       string                        `validate:"required,min=3,max=200"`
	Description null.String                   `validate:"omitnil,max=1000"`
	IsEnabled   bool                          `validate:"omitempty"`
	AutoApply   bool                          `validate:"omitempty"`
	Group       string                        `validate:"required"`
	Priority    int32                         `validate:"omitempty"`
	Data        json.RawMessage               `validate:"omitempty"`
	DateStarted time.Time                     `validate:"required"`
	DateEnded   null.Time                     `validate:"omitnil,gtfield=DateStarted"`
	Refs        []promotionmodel.PromotionRef `validate:"dive"`
}

// CreatePromotion creates a new promotion with the given parameters and refs.
func (b *PromotionHandler) CreatePromotion(
	ctx restate.Context,
	params CreatePromotionParams,
) (promotionmodel.Promotion, error) {
	var zero promotionmodel.Promotion

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create promotion: %w", err)
	}

	dbPromo, err := b.storage.Querier().CreateDefaultPromotion(ctx, promotiondb.CreateDefaultPromotionParams{
		Code:        params.Code,
		OwnerID:     uuid.NullUUID{UUID: params.Account.ID, Valid: true},
		Type:        promotiondb.PromotionType(params.Type),
		Title:       params.Title,
		Description: params.Description,
		IsEnabled:   params.IsEnabled,
		AutoApply:   params.AutoApply,
		Group:       params.Group,
		Data:        params.Data,
		DateStarted: params.DateStarted,
		DateEnded:   params.DateEnded,
	})
	if err != nil {
		return zero, fmt.Errorf("db create promotion: %w", err)
	}

	if err := createRefs(ctx, b.storage, dbPromo.ID, params.Refs); err != nil {
		return zero, fmt.Errorf("db create promotion: %w", err)
	}

	return mapPromotion(dbPromo, nil), nil
}

// --- Update ---

type UpdatePromotionParams struct {
	Account accountmodel.AuthenticatedAccount

	ID              uuid.UUID                      `validate:"required"`
	Code            null.String                    `validate:"omitnil"`
	OwnerID         uuid.NullUUID                  `validate:"omitnil"`
	NullOwnerID     bool                           `validate:"omitempty"`
	Title           null.String                    `validate:"omitnil"`
	Description     null.String                    `validate:"omitnil"`
	NullDescription bool                           `validate:"omitempty"`
	IsEnabled       null.Bool                      `validate:"omitnil"`
	AutoApply       null.Bool                      `validate:"omitnil"`
	Group           null.String                    `validate:"omitnil"`
	Priority        null.Int32                     `validate:"omitnil"`
	Data            json.RawMessage                `validate:"omitempty"`
	NullData        bool                           `validate:"omitempty"`
	DateStarted     null.Time                      `validate:"omitnil"`
	DateEnded       null.Time                      `validate:"omitnil"`
	NullDateEnded   bool                           `validate:"omitempty"`
	Refs            *[]promotionmodel.PromotionRef `validate:"omitnil"`
}

// UpdatePromotion updates the specified promotion fields and optionally replaces its refs.
func (s *PromotionHandler) UpdatePromotion(
	ctx restate.Context,
	params UpdatePromotionParams,
) (promotionmodel.Promotion, error) {
	var zero promotionmodel.Promotion

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update promotion: %w", err)
	}

	dbPromo, err := s.storage.Querier().UpdatePromotion(ctx, promotiondb.UpdatePromotionParams{
		ID:              params.ID,
		Code:            params.Code,
		OwnerID:         params.OwnerID,
		NullOwnerID:     params.NullOwnerID,
		Title:           params.Title,
		Description:     params.Description,
		NullDescription: params.NullDescription,
		IsEnabled:       params.IsEnabled,
		AutoApply:       params.AutoApply,
		Group:           params.Group,
		Data:            params.Data,
		NullData:        params.NullData,
		DateStarted:     params.DateStarted,
		DateEnded:       params.DateEnded,
		NullDateEnded:   params.NullDateEnded,
	})
	if err != nil {
		return zero, fmt.Errorf("db update promotion: %w", err)
	}

	if params.Refs != nil {
		if err := s.storage.Querier().DeleteRef(ctx, promotiondb.DeleteRefParams{
			PromotionID: []uuid.UUID{params.ID},
		}); err != nil {
			return zero, fmt.Errorf("db update promotion: %w", err)
		}
		if err := createRefs(ctx, s.storage, dbPromo.ID, *params.Refs); err != nil {
			return zero, fmt.Errorf("db update promotion: %w", err)
		}
	}

	return mapPromotion(dbPromo, nil), nil
}

// --- Delete ---

type DeletePromotionParams struct {
	Account accountmodel.AuthenticatedAccount
	ID      uuid.UUID
}

// DeletePromotion deletes the promotion with the given ID.
func (s *PromotionHandler) DeletePromotion(ctx restate.Context, params DeletePromotionParams) error {
	return s.storage.Querier().DeletePromotion(ctx, promotiondb.DeletePromotionParams{
		ID: []uuid.UUID{params.ID},
	})
}

// --- Helpers ---

// createRefs bulk-inserts refs for a promotion. No-op if refs is empty.
func createRefs(
	ctx context.Context,
	storage PromotionStorage,
	promoID uuid.UUID,
	refs []promotionmodel.PromotionRef,
) error {
	if len(refs) == 0 {
		return nil
	}
	_, err := storage.Querier().
		CreateCopyDefaultRef(ctx, lo.Map(refs, func(r promotionmodel.PromotionRef, _ int) promotiondb.CreateCopyDefaultRefParams {
			return promotiondb.CreateCopyDefaultRefParams{
				PromotionID: promoID,
				RefType:     promotiondb.PromotionRefType(r.RefType),
				RefID:       r.RefID,
			}
		}))
	if err != nil {
		return fmt.Errorf("db create promotion refs: %w", err)
	}
	return nil
}

// mapPromotion maps a DB row + refs to the domain model.
func mapPromotion(p promotiondb.PromotionPromotion, refs []promotiondb.PromotionRef) promotionmodel.Promotion {
	return promotionmodel.Promotion{
		ID:          p.ID,
		Code:        p.Code,
		OwnerID:     p.OwnerID,
		Type:        promotionmodel.Type(p.Type),
		Title:       p.Title,
		Description: p.Description,
		IsEnabled:   p.IsEnabled,
		AutoApply:   p.AutoApply,
		Group:       p.Group,
		Data:        p.Data,
		DateStarted: p.DateStarted,
		DateEnded:   p.DateEnded,
		DateCreated: p.DateCreated,
		DateUpdated: p.DateUpdated,
		Refs: lo.Map(refs, func(r promotiondb.PromotionRef, _ int) promotionmodel.PromotionRef {
			return promotionmodel.PromotionRef{
				RefType: promotionmodel.RefType(r.RefType),
				RefID:   r.RefID,
			}
		}),
	}
}

// parseDiscountData unmarshals JSONB data into DiscountData.
// Returns nil on empty/invalid data and logs a warning.
func (s *PromotionHandler) parseDiscountData(promoID uuid.UUID, data json.RawMessage) *DiscountData {
	if len(data) == 0 {
		return nil
	}
	var d DiscountData
	if err := json.Unmarshal(data, &d); err != nil {
		s.logger.Warn("failed to parse promotion data",
			"promotion_id", promoID,
			"error", err,
		)
		return nil
	}
	return &d
}
