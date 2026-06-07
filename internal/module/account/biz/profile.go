package accountbiz

import (
	stderrors "errors"
	"fmt"

	restate "github.com/restatedev/sdk-go"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	sharedcurrency "shopnexus-server/internal/shared/currency"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	"github.com/samber/lo"
)

type ListProfileParams struct {
	paginate.Params

	Issuer     accountmodel.AuthenticatedAccount // Who is requesting the profiles
	AccountIDs []uuid.UUID                       `validate:"dive,required"`
}

// ListProfile returns a paginated list of profiles for the given account IDs.
func (b *AccountHandler) ListProfile(
	ctx restate.Context,
	params ListProfileParams,
) (paginate.PaginateResult[accountmodel.Profile], error) {
	var result paginate.PaginateResult[accountmodel.Profile]
	if err := validator.Validate(params); err != nil {
		return result, fmt.Errorf("validate list profile params: %w", err)
	}

	listProfile, err := b.storage.Querier().ListCountProfile(ctx, accountdb.ListCountProfileParams{
		ID:     params.AccountIDs,
		Limit:  params.Limit,
		Offset: params.Offset(),
	})
	if err != nil {
		return result, fmt.Errorf("db list count profile: %w", err)
	}

	var total null.Int64
	if len(listProfile) > 0 {
		total.SetValid(listProfile[0].TotalCount)
	}

	dbProfiles := lo.Map(listProfile, func(row accountdb.ListCountProfileRow, _ int) accountdb.AccountProfile {
		return row.AccountProfile
	})

	listAccount, err := b.storage.Querier().ListAccount(ctx, accountdb.ListAccountParams{
		ID: lo.Map(params.AccountIDs, func(id uuid.UUID, _ int) uuid.UUID { return id }),
	})
	if err != nil {
		return result, fmt.Errorf("db list account: %w", err)
	}

	accountMap := lo.KeyBy(listAccount, func(account accountdb.AccountAccount) uuid.UUID {
		return account.ID
	})

	profiles := make([]accountmodel.Profile, 0, len(dbProfiles))
	for _, dbProfile := range dbProfiles {
		account := accountMap[dbProfile.ID]
		profiles = append(profiles, b.mapProfile(ctx, account, dbProfile))
	}

	return paginate.PaginateResult[accountmodel.Profile]{
		PageParams: params.Params,
		Data:       profiles,
		Total:      total,
	}, nil
}

type GetProfileParams struct {
	Issuer    accountmodel.AuthenticatedAccount // Who is requesting the profile
	AccountID uuid.UUID
}

// GetProfile returns the profile for the given account ID. If the profile
// row is missing (e.g. an account inserted directly via SQL such as an admin
// seed), the account row is still returned with empty profile defaults so
// /me continues to work for non-storefront users.
func (b *AccountHandler) GetProfile(ctx restate.Context, params GetProfileParams) (accountmodel.Profile, error) {
	var zero accountmodel.Profile

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get profile params: %w", err)
	}

	account, err := b.storage.Querier().GetAccount(ctx, accountdb.GetAccountParams{
		ID: uuid.NullUUID{UUID: params.AccountID, Valid: true},
	})
	if err != nil {
		return zero, fmt.Errorf("db get account: %w", err)
	}

	profile, err := b.storage.Querier().GetProfile(ctx, accountdb.GetProfileParams{
		ID: uuid.NullUUID{UUID: params.AccountID, Valid: true},
	})
	if err != nil {
		if stderrors.Is(err, pgx.ErrNoRows) {
			return b.mapProfile(ctx, account, accountdb.AccountProfile{ID: account.ID}), nil
		}
		return zero, fmt.Errorf("db get profile: %w", err)
	}

	m := b.mapProfile(ctx, account, profile)
	return m, nil
}

type UpdateProfileParams struct {
	Issuer    accountmodel.AuthenticatedAccount // Who is performing the update
	AccountID uuid.UUID                         // Whose profile to be updated

	// Account base fields
	Status   accountmodel.Status
	Username null.String
	Phone    null.String
	Email    null.String

	// Profile fields
	Gender           accountmodel.Gender
	Name             null.String
	DateOfBirth      null.Time
	AvatarRsID       uuid.NullUUID
	DefaultContactID uuid.NullUUID

	// Description
	Description null.String
}

// UpdateProfile updates the account and profile fields for the given account.
func (b *AccountHandler) UpdateProfile(ctx restate.Context, params UpdateProfileParams) (accountmodel.Profile, error) {
	var zero accountmodel.Profile

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update profile params: %w", err)
	}

	account, err := b.storage.Querier().UpdateAccount(ctx, accountdb.UpdateAccountParams{
		ID:       params.AccountID,
		Status:   accountdb.NullAccountStatus{AccountStatus: accountdb.AccountStatus(params.Status), Valid: params.Status != ""},
		Username: params.Username,
		Phone:    params.Phone,
		Email:    params.Email,
	})
	if err != nil {
		return zero, fmt.Errorf("db update account: %w", err)
	}

	profile, err := b.storage.Querier().UpdateProfile(ctx, accountdb.UpdateProfileParams{
		ID:          params.AccountID,
		Gender:      accountdb.NullAccountGender{AccountGender: accountdb.AccountGender(params.Gender), Valid: params.Gender != ""},
		Name:        params.Name,
		DateOfBirth: params.DateOfBirth,
		AvatarRsID:  params.AvatarRsID,
	})
	if err != nil {
		return zero, fmt.Errorf("db update profile: %w", err)
	}

	if params.DefaultContactID.Valid {
		if err := b.storage.Querier().SetAccountDefaultContact(ctx, accountdb.SetAccountDefaultContactParams{
			ID:               params.AccountID,
			DefaultContactID: params.DefaultContactID,
		}); err != nil {
			return zero, fmt.Errorf("db set account default contact: %w", err)
		}
	}

	m := b.mapProfile(ctx, account, profile)
	return m, nil
}

// mapProfile maps DB account + profile rows to the model type.
func (b *AccountHandler) mapProfile(
	ctx restate.Context,
	account accountdb.AccountAccount,
	profile accountdb.AccountProfile,
) accountmodel.Profile {
	avatar, _ := b.common.GetResourceByID(ctx, profile.AvatarRsID.UUID)
	var url null.String
	if avatar != nil {
		url.SetValid(avatar.Url)
	}

	currency, _ := sharedcurrency.Infer(profile.Country)

	defaultContactID, _ := b.storage.Querier().GetAccountDefaults(ctx, account.ID)

	return accountmodel.Profile{
		ID:          account.ID,
		DateCreated: account.DateCreated,

		Status:   accountmodel.Status(account.Status),
		Role:     accountmodel.Role(account.Role),
		Phone:    account.Phone,
		Email:    account.Email,
		Username: account.Username,

		Gender:           accountmodel.NullGender{Value: accountmodel.Gender(profile.Gender.AccountGender), Valid: profile.Gender.Valid},
		Name:             null.StringFrom(profile.Name),
		DateOfBirth:      profile.DateOfBirth,
		EmailVerified:    profile.EmailVerified,
		PhoneVerified:    profile.PhoneVerified,
		Country:          profile.Country,
		Currency:         currency,
		DefaultContactID: defaultContactID,
		AvatarURL:        url,
	}
}

type UpdateCountryParams struct {
	AccountID uuid.UUID `validate:"required"`
	Country   string    `validate:"required,len=2,uppercase"`
}

// UpdateCountry sets the profile country. Fails with a conflict error if the
// caller's wallet balance is non-zero, since changing country implies changing
// wallet currency.
func (b *AccountHandler) UpdateCountry(ctx restate.Context, params UpdateCountryParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate update country params: %w", err)
	}
	if _, err := sharedcurrency.Infer(params.Country); err != nil {
		return fmt.Errorf("infer currency from country: %w", err)
	}

	if err := restate.RunVoid(ctx, func(ctx restate.RunContext) error {
		_, err := b.storage.Querier().UpdateProfileCountry(ctx, accountdb.UpdateProfileCountryParams{
			ID:      params.AccountID,
			Country: params.Country,
		})
		return err
	}); err != nil {
		return fmt.Errorf("db update profile country: %w", err)
	}
	return nil
}
