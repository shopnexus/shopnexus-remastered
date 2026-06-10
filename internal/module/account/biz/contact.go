package accountbiz

import (
	"context"
	"fmt"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
	accountmodel "shopnexus-server/internal/module/account/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

// validateAddressCountry geocodes the given address via the
// common module and rejects the request if the resolved country does not
// match the owner's profile country. Used by CreateContact/UpdateContact so
// a user can only register addresses that resolve to their own country.
func (b *AccountHandler) validateAddressCountry(
	ctx context.Context,
	accountID uuid.UUID,
	address string,
) error {
	profile, err := b.storage.Querier().GetProfile(ctx, accountdb.GetProfileParams{
		ID: uuid.NullUUID{UUID: accountID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("load profile for address check: %w", err)
	}

	resolvedCountry, err := b.common.ResolveCountry(ctx, address)
	if err != nil {
		return fmt.Errorf("resolve country for address: %w", err)
	}

	if resolvedCountry != profile.Country {
		return accountmodel.ErrContactAddressCountryMismatch.Fmt(resolvedCountry, profile.Country)
	}
	return nil
}

type ListContactParams struct {
	AccountID []uuid.UUID `validate:"dive,required"`
	ID        []uuid.UUID `validate:"omitempty,dive"`
}

// ListContact returns contacts matching the given account and contact IDs.
func (b *AccountHandler) ListContact(
	ctx context.Context,
	params ListContactParams,
) ([]accountdb.AccountContact, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list contact: %w", err)
	}

	res, err := b.storage.Querier().ListContact(ctx, accountdb.ListContactParams{
		AccountId: params.AccountID,
		Id:        params.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("db list contact: %w", err)
	}

	return res.Data, nil
}

type GetContactParams struct {
	Account   accountmodel.AuthenticatedAccount
	ContactID uuid.UUID `validate:"required"`
}

// GetContact returns a single contact by ID for the authenticated account.
func (b *AccountHandler) GetContact(ctx context.Context, params GetContactParams) (accountdb.AccountContact, error) {
	var zero accountdb.AccountContact

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate get contact: %w", err)
	}

	result, err := b.ListContact(ctx, ListContactParams{
		AccountID: []uuid.UUID{params.Account.ID},
		ID:        []uuid.UUID{params.ContactID},
	})
	if err != nil {
		return zero, fmt.Errorf("get contact: %w", err)
	}

	if len(result) == 0 {
		return zero, accountmodel.ErrContactNotFound
	}

	return result[0], nil
}

type CreateContactParams struct {
	Account     accountmodel.AuthenticatedAccount
	FullName    string                   `validate:"required"`
	Phone       string                   `validate:"required"`
	Address     string                   `validate:"required"`
	AddressType accountmodel.AddressType `validate:"required,validateFn=Valid"`
	Latitude    null.Float               `validate:"omitnil"`
	Longitude   null.Float               `validate:"omitnil"`
}

// CreateContact creates a new contact for the authenticated account.
func (b *AccountHandler) CreateContact(
	ctx restate.Context,
	params CreateContactParams,
) (accountdb.AccountContact, error) {
	var zero accountdb.AccountContact
	var err error

	if err = validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create contact: %w", err)
	}

	// decision: geocode + validate the address country (cross-module read).
	if err = b.validateAddressCountry(ctx, params.Account.ID, params.Address); err != nil {
		return zero, fmt.Errorf("validate address: %w", err)
	}

	// execution: create the contact, promoting it to default if it is the first.
	return restate.Run(ctx, func(rctx restate.RunContext) (accountdb.AccountContact, error) {
		txStorage, err := b.storage.BeginTx(rctx)
		if err != nil {
			return zero, fmt.Errorf("begin transaction: %w", err)
		}
		defer txStorage.Rollback(rctx)

		total, err := txStorage.Querier().CountContact(rctx, accountdb.CountContactParams{
			AccountID: []uuid.UUID{params.Account.ID},
		})
		if err != nil {
			return zero, fmt.Errorf("db create contact: %w", err)
		}

		dbContact, err := txStorage.Querier().CreateDefaultContact(rctx, accountdb.CreateDefaultContactParams{
			AccountID:   params.Account.ID,
			FullName:    params.FullName,
			Phone:       params.Phone,
			Address:     params.Address,
			AddressType: accountdb.AccountAddressType(params.AddressType),
			Latitude:    params.Latitude.Float64,
			Longitude:   params.Longitude.Float64,
		})
		if err != nil {
			return zero, fmt.Errorf("db create contact: %w", err)
		}

		if total == 0 {
			if err = txStorage.Querier().SetAccountDefaultContact(rctx, accountdb.SetAccountDefaultContactParams{
				ID:               params.Account.ID,
				DefaultContactID: uuid.NullUUID{UUID: dbContact.ID, Valid: true},
			}); err != nil {
				return zero, fmt.Errorf("set default contact: %w", err)
			}
		}

		if err = txStorage.Commit(rctx); err != nil {
			return zero, fmt.Errorf("commit transaction: %w", err)
		}

		return dbContact, nil
	})
}

type UpdateContactParams struct {
	Account     accountmodel.AuthenticatedAccount
	ContactID   uuid.UUID                    `validate:"required"`
	FullName    null.String                  `validate:"omitnil"`
	Phone       null.String                  `validate:"omitnil"`
	Address     null.String                  `validate:"omitnil"`
	AddressType accountmodel.NullAddressType `validate:"omitnil,validateFn=Valid"`
	Latitude    null.Float                   `validate:"omitnil"`
	Longitude   null.Float                   `validate:"omitnil"`

	PhoneVerified null.Bool `validate:"omitnil"`
}

// UpdateContact updates the specified contact fields.
func (b *AccountHandler) UpdateContact(
	ctx restate.Context,
	params UpdateContactParams,
) (accountdb.AccountContact, error) {
	var zero accountdb.AccountContact

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update contact: %w", err)
	}

	// decision: geocode + validate the address country (cross-module read).
	if params.Address.Valid && params.Address.String != "" {
		if err := b.validateAddressCountry(ctx, params.Account.ID, params.Address.String); err != nil {
			return zero, fmt.Errorf("validate address: %w", err)
		}
	}

	var addressType accountdb.NullAccountAddressType
	if params.AddressType.Valid {
		addressType = accountdb.NullAccountAddressType{
			AccountAddressType: accountdb.AccountAddressType(params.AddressType.Value),
			Valid:              true,
		}
	}

	// execution: update the contact.
	return restate.Run(ctx, func(rctx restate.RunContext) (accountdb.AccountContact, error) {
		updatedContact, err := b.storage.Querier().UpdateContact(rctx, accountdb.UpdateContactParams{
			ID:            params.ContactID,
			FullName:      params.FullName,
			Phone:         params.Phone,
			Address:       params.Address,
			AddressType:   addressType,
			Latitude:      params.Latitude,
			Longitude:     params.Longitude,
			PhoneVerified: params.PhoneVerified,
		})
		if err != nil {
			return zero, fmt.Errorf("db update contact: %w", err)
		}
		return updatedContact, nil
	})
}

type DeleteContactParams struct {
	Account   accountmodel.AuthenticatedAccount
	ContactID uuid.UUID
}

// DeleteContact removes a contact belonging to the authenticated account.
// Cannot delete the last remaining contact. If the default contact is deleted,
// the most recently created remaining contact becomes the new default.
func (b *AccountHandler) DeleteContact(ctx restate.Context, params DeleteContactParams) error {
	// execution: delete the contact, reassigning the default if needed.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		txStorage, err := b.storage.BeginTx(rctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer txStorage.Rollback(rctx)

		total, err := txStorage.Querier().CountContact(rctx, accountdb.CountContactParams{
			AccountID: []uuid.UUID{params.Account.ID},
		})
		if err != nil {
			return fmt.Errorf("db count contact: %w", err)
		}
		if total <= 1 {
			return accountmodel.ErrCannotDeleteLastContact
		}

		// Check if we're deleting the default contact
		defaultContactID, err := txStorage.Querier().GetAccountDefaults(rctx, params.Account.ID)
		if err != nil {
			return fmt.Errorf("db get account defaults: %w", err)
		}
		isDefault := defaultContactID.Valid && defaultContactID.UUID == params.ContactID

		// Delete the contact
		if err = txStorage.Querier().DeleteContact(rctx, accountdb.DeleteContactParams{
			ID:        []uuid.UUID{params.ContactID},
			AccountID: []uuid.UUID{params.Account.ID},
		}); err != nil {
			return fmt.Errorf("db delete contact: %w", err)
		}

		// If we deleted the default, reassign to the most recent remaining contact
		if isDefault {
			remaining, err := txStorage.Querier().ListContact(rctx, accountdb.ListContactParams{
				AccountId: []uuid.UUID{params.Account.ID},
			})
			if err != nil {
				return fmt.Errorf("db list contact: %w", err)
			}
			if len(remaining.Data) > 0 {
				if err = txStorage.Querier().SetAccountDefaultContact(rctx, accountdb.SetAccountDefaultContactParams{
					ID:               params.Account.ID,
					DefaultContactID: uuid.NullUUID{UUID: remaining.Data[0].ID, Valid: true},
				}); err != nil {
					return fmt.Errorf("db set account default contact: %w", err)
				}
			}
		}

		if err = txStorage.Commit(rctx); err != nil {
			return fmt.Errorf("commit transaction: %w", err)
		}

		return nil
	})
}

// GetDefaultContact returns the default contact for each of the given account IDs.
func (b *AccountHandler) GetDefaultContact(
	ctx context.Context,
	accountIDs []uuid.UUID,
) (map[uuid.UUID]accountdb.AccountContact, error) {
	contacts, err := b.storage.Querier().ListDefaultContact(ctx, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("db list default contact: %w", err)
	}
	if len(contacts) != len(lo.Uniq(accountIDs)) {
		return nil, accountmodel.ErrNoDefaultContact
	}

	return lo.KeyBy(contacts, func(c accountdb.AccountContact) uuid.UUID { return c.AccountID }), nil
}
