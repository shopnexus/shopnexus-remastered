package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
)

// contactColumns reads the coordinate back out of the geography column as two
// numbers. The column is the storage type PostGIS needs for a distance query; the
// API speaks latitude and longitude.
const contactColumns = `id, account_id, full_name, phone, phone_verified, address_type::text,
	       is_default_delivery, is_default_pickup, country, province_code, province_name,
	       COALESCE(district_code, ''), COALESCE(district_name, ''), ward_code, ward_name,
	       COALESCE(postal_code, ''), address, COALESCE(address_detail, ''),
	       ST_Y(location::geometry), ST_X(location::geometry), provider_codes, created_at`

// locationExpr builds the geography value from the two named args, or NULL when the
// address could not be geocoded. The cast on the parameter is what lets Postgres see
// its type inside the CASE.
const locationExpr = `CASE WHEN @latitude::double precision IS NULL THEN NULL
	                  ELSE ST_SetSRID(ST_MakePoint(@longitude::double precision, @latitude::double precision), 4326)::geography END`

func scanContact(row pgx.Row) (domain.Contact, error) {
	var c domain.Contact
	err := row.Scan(&c.ID, &c.AccountID, &c.FullName, &c.Phone, &c.PhoneVerified, &c.AddressType,
		&c.IsDefaultDelivery, &c.IsDefaultPickup, &c.Country, &c.ProvinceCode, &c.ProvinceName,
		&c.DistrictCode, &c.DistrictName, &c.WardCode, &c.WardName, &c.PostalCode,
		&c.Address, &c.AddressDetail, &c.Latitude, &c.Longitude, &c.ProviderCodes, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Contact{}, domain.ErrContactNotFound
	}
	if err != nil {
		return domain.Contact{}, fmt.Errorf("db scan contact: %w", err)
	}
	return c, nil
}

func contactArgs(c domain.Contact) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id":                  c.ID,
		"account_id":          c.AccountID,
		"full_name":           c.FullName,
		"phone":               c.Phone,
		"phone_verified":      c.PhoneVerified,
		"address_type":        string(c.AddressType),
		"is_default_delivery": c.IsDefaultDelivery,
		"is_default_pickup":   c.IsDefaultPickup,
		"country":             c.Country,
		"province_code":       c.ProvinceCode,
		"province_name":       c.ProvinceName,
		"district_code":       nullText(c.DistrictCode),
		"district_name":       nullText(c.DistrictName),
		"ward_code":           c.WardCode,
		"ward_name":           c.WardName,
		"postal_code":         nullText(c.PostalCode),
		"address":             c.Address,
		"address_detail":      nullText(c.AddressDetail),
		"latitude":            c.Latitude,
		"longitude":           c.Longitude,
		"provider_codes":      jsonObject(c.ProviderCodes),
	}
}

// clearDefaults gives up the account's current defaults for whichever roles the
// incoming row claims. The partial unique indexes allow one default per role per
// account, so the previous holder has to be cleared in the same transaction —
// otherwise the write fails instead of moving the default.
func clearDefaults(ctx context.Context, tx pgx.Tx, c domain.Contact) error {
	if c.IsDefaultDelivery {
		const q = `UPDATE contact SET is_default_delivery = false
		           WHERE account_id = @account_id AND is_default_delivery AND id <> @id`
		if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"account_id": c.AccountID, "id": c.ID}); err != nil {
			return fmt.Errorf("db clear default delivery contact: %w", err)
		}
	}
	if c.IsDefaultPickup {
		const q = `UPDATE contact SET is_default_pickup = false
		           WHERE account_id = @account_id AND is_default_pickup AND id <> @id`
		if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"account_id": c.AccountID, "id": c.ID}); err != nil {
			return fmt.Errorf("db clear default pickup contact: %w", err)
		}
	}
	return nil
}

func (r *Repo) InsertContact(ctx context.Context, c *domain.Contact) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		if err := clearDefaults(ctx, tx, *c); err != nil {
			return err
		}
		const q = `INSERT INTO contact (account_id, full_name, phone, phone_verified, address_type,
		                        is_default_delivery, is_default_pickup, country, province_code, province_name,
		                        district_code, district_name, ward_code, ward_name, postal_code,
		                        provider_codes, address, address_detail, location)
		           VALUES (@account_id, @full_name, @phone, @phone_verified, @address_type,
		                   @is_default_delivery, @is_default_pickup, @country, @province_code, @province_name,
		                   @district_code, @district_name, @ward_code, @ward_name, @postal_code,
		                   @provider_codes, @address, @address_detail, ` + locationExpr + `)
		           RETURNING id, created_at`
		if err := tx.QueryRow(ctx, q, contactArgs(*c)).Scan(&c.ID, &c.CreatedAt); err != nil {
			if isForeignKeyViolation(err) {
				return domain.ErrAccountNotFound
			}
			return fmt.Errorf("db insert contact: %w", err)
		}
		return nil
	})
}

func (r *Repo) UpdateContact(ctx context.Context, c domain.Contact) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		if err := clearDefaults(ctx, tx, c); err != nil {
			return err
		}
		const q = `UPDATE contact
		           SET full_name = @full_name, phone = @phone, phone_verified = @phone_verified,
		               address_type = @address_type, is_default_delivery = @is_default_delivery,
		               is_default_pickup = @is_default_pickup, country = @country,
		               province_code = @province_code, province_name = @province_name,
		               district_code = @district_code, district_name = @district_name,
		               ward_code = @ward_code, ward_name = @ward_name, postal_code = @postal_code,
		               provider_codes = @provider_codes, address = @address,
		               address_detail = @address_detail, location = ` + locationExpr + `
		           WHERE id = @id`
		tag, err := tx.Exec(ctx, q, contactArgs(c))
		if err != nil {
			return fmt.Errorf("db update contact: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrContactNotFound
		}
		return nil
	})
}

func (r *Repo) FindContact(ctx context.Context, id int64) (domain.Contact, error) {
	q := `SELECT ` + contactColumns + ` FROM contact WHERE id = @id`
	return scanContact(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// ListContacts puts the defaults first: a checkout form wants the address it is going
// to preselect at the top of the list.
func (r *Repo) ListContacts(ctx context.Context, accountID int64) ([]domain.Contact, error) {
	q := `SELECT ` + contactColumns + `
	      FROM contact WHERE account_id = @account_id
	      ORDER BY is_default_delivery DESC, is_default_pickup DESC, created_at DESC`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query contacts: %w", err)
	}
	defer rows.Close()

	var out []domain.Contact
	for rows.Next() {
		var c domain.Contact
		if err := rows.Scan(&c.ID, &c.AccountID, &c.FullName, &c.Phone, &c.PhoneVerified, &c.AddressType,
			&c.IsDefaultDelivery, &c.IsDefaultPickup, &c.Country, &c.ProvinceCode, &c.ProvinceName,
			&c.DistrictCode, &c.DistrictName, &c.WardCode, &c.WardName, &c.PostalCode,
			&c.Address, &c.AddressDetail, &c.Latitude, &c.Longitude, &c.ProviderCodes, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan contact row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate contacts: %w", err)
	}
	return out, nil
}

func (r *Repo) DeleteContact(ctx context.Context, id int64) error {
	const q = `DELETE FROM contact WHERE id = @id`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return fmt.Errorf("db delete contact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrContactNotFound
	}
	return nil
}
