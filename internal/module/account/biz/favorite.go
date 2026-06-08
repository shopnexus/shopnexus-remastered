package accountbiz

import (
	"fmt"

	restate "github.com/restatedev/sdk-go"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/repolist"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
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

	// Check if already favorited
	existing, err := b.storage.Querier().GetFavorite(ctx, accountdb.GetFavoriteParams{
		AccountID: uuid.NullUUID{UUID: params.Account.ID, Valid: true},
		SpuID:     uuid.NullUUID{UUID: params.SpuID, Valid: true},
	})
	if err == nil {
		return existing, nil // Already favorited
	}

	result, err := b.storage.Querier().CreateDefaultFavorite(ctx, accountdb.CreateDefaultFavoriteParams{
		AccountID: params.Account.ID,
		SpuID:     params.SpuID,
	})
	if err != nil {
		return zero, fmt.Errorf("db create default favorite: %w", err)
	}

	return result, nil
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
	if err := b.storage.Querier().DeleteFavorite(ctx, accountdb.DeleteFavoriteParams{
		AccountID: []uuid.UUID{params.Account.ID},
		SpuID:     []uuid.UUID{params.SpuID},
	}); err != nil {
		return fmt.Errorf("db delete favorite: %w", err)
	}

	return nil
}

type ListFavoriteParams struct {
	paginate.Params

	Account accountmodel.AuthenticatedAccount
}

// ListFavorite returns a paginated list of the account's favorited products.
func (b *AccountHandler) ListFavorite(
	ctx restate.Context,
	params ListFavoriteParams,
) (paginate.PaginateResult[accountdb.AccountFavorite], error) {
	var zero paginate.PaginateResult[accountdb.AccountFavorite]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list favorite params: %w", err)
	}

	params.Params = params.Constrain()

	res, err := b.storage.Querier().ListFavorite(ctx, repolist.FromParams(params.Params), accountdb.ListFavoriteFilter{
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
func (b *AccountHandler) CheckFavorites(ctx restate.Context, params CheckFavoritesParams) (map[uuid.UUID]bool, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate check favorites params: %w", err)
	}

	accountID := params.AccountID
	spuIDs := params.SpuIDs
	if len(spuIDs) == 0 {
		return nil, nil
	}

	res, err := b.storage.Querier().ListFavorite(ctx, repolist.Request{}, accountdb.ListFavoriteFilter{
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
