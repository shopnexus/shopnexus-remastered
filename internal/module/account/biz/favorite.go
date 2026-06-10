package accountbiz

import (
	"context"
	"fmt"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

type AddFavoriteParams struct {
	Account accountmodel.AuthenticatedAccount
	SpuID   uuid.UUID `validate:"required"`
}

// AddFavorite adds a product SPU to the account's favorites list.
func (b *AccountHandler) AddFavorite(ctx restate.Context, params AddFavoriteParams) (accountdb.AccountFavorite, error) {
	var zero accountdb.AccountFavorite

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate add favorite params: %w", err)
	}

	// decision: return the existing favorite if present, else create it.
	return restate.Run(ctx, func(rctx restate.RunContext) (accountdb.AccountFavorite, error) {
		existing, err := b.storage.Querier().GetFavorite(rctx, accountdb.GetFavoriteParams{
			AccountID: uuid.NullUUID{UUID: params.Account.ID, Valid: true},
			SpuID:     uuid.NullUUID{UUID: params.SpuID, Valid: true},
		})
		if err == nil {
			return existing, nil // Already favorited
		}

		result, err := b.storage.Querier().CreateDefaultFavorite(rctx, accountdb.CreateDefaultFavoriteParams{
			AccountID: params.Account.ID,
			SpuID:     params.SpuID,
		})
		if err != nil {
			return zero, fmt.Errorf("db create default favorite: %w", err)
		}

		return result, nil
	})
}

type RemoveFavoriteParams struct {
	Account accountmodel.AuthenticatedAccount
	SpuID   uuid.UUID `validate:"required"`
}

// RemoveFavorite removes a product SPU from the account's favorites list.
func (b *AccountHandler) RemoveFavorite(ctx restate.Context, params RemoveFavoriteParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate remove favorite params: %w", err)
	}
	// execution: remove the favorite.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		if err := b.storage.Querier().DeleteFavorite(rctx, accountdb.DeleteFavoriteParams{
			AccountID: []uuid.UUID{params.Account.ID},
			SpuID:     []uuid.UUID{params.SpuID},
		}); err != nil {
			return fmt.Errorf("db delete favorite: %w", err)
		}
		return nil
	})
}

type ListFavoriteParams struct {
	paginate.Params

	Account accountmodel.AuthenticatedAccount
}

// ListFavorite returns a paginated list of the account's favorited products.
func (b *AccountHandler) ListFavorite(
	ctx context.Context,
	params ListFavoriteParams,
) (paginate.PaginateResult[accountdb.AccountFavorite], error) {
	var zero paginate.PaginateResult[accountdb.AccountFavorite]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list favorite params: %w", err)
	}

	res, err := b.storage.Querier().ListFavorite(ctx, accountdb.ListFavoriteParams{
		Params:    params.Params,
		AccountId: []uuid.UUID{params.Account.ID},
	})
	if err != nil {
		return zero, fmt.Errorf("db list favorite: %w", err)
	}

	return res, nil
}

type CheckFavoritesParams struct {
	AccountID uuid.UUID
	SpuIDs    []uuid.UUID
}

// CheckFavorites checks which SPU IDs are favorited by the given account.
func (b *AccountHandler) CheckFavorites(ctx context.Context, params CheckFavoritesParams) (map[uuid.UUID]bool, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate check favorites params: %w", err)
	}

	accountID := params.AccountID
	spuIDs := params.SpuIDs
	if len(spuIDs) == 0 {
		return nil, nil
	}

	res, err := b.storage.Querier().ListFavorite(ctx, accountdb.ListFavoriteParams{
		AccountId: []uuid.UUID{accountID},
		SpuId:     spuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db list favorite: %w", err)
	}

	result := make(map[uuid.UUID]bool, len(res.Data))
	for _, row := range res.Data {
		result[row.SpuID] = true
	}
	return result, nil
}
