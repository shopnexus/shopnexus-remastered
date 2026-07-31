// Package postgres implements the account port.Repository with pgx named args and
// hand-written SQL.
//
// All SQL is unqualified: the pool sets search_path to this module's schema, so a
// table name is enough and the module can later move to its own database without a
// rewrite.
package postgres

import (
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common/dbx"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var _ port.Repository = (*Repo)(nil)

// accountColumns is every column of the root, display half included — one table, so one
// read. A nullable column is scanned straight into the domain's pointer field: NULL
// arrives as nil, the same "not set" the entity uses. Enum columns are cast to text
// because the domain's types are strings.
const accountColumns = `id, version, status::text, role::text, phone, email, username,
	       password_hash, email_verified, created_at, suspended_until, suspension_reason,
	       name, description, gender::text, date_of_birth, avatar_resource_id,
	       country, locale, timezone`

// scanAccount reads accountColumns, in that order.
func scanAccount(row pgx.Row) (*domain.Account, error) {
	var a domain.Account
	// Gender goes through a *string: the column is cast to text, and converting the
	// pointer afterwards keeps the scan off the driver's handling of named types.
	var gender *string
	err := row.Scan(&a.ID, &a.Version, &a.Status, &a.Role, &a.Phone, &a.Email, &a.Username,
		&a.PasswordHash, &a.EmailVerified, &a.CreatedAt, &a.SuspendedUntil, &a.SuspensionReason,
		&a.Profile.Name, &a.Profile.Description, &gender, &a.Profile.DateOfBirth,
		&a.Profile.AvatarResourceID, &a.Profile.Country, &a.Profile.Locale, &a.Profile.Timezone)
	if dbx.IsNoRows(err) {
		return nil, domain.ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db scan account: %w", err)
	}
	a.Profile.ID = a.ID
	a.Profile.Gender = (*domain.Gender)(gender)
	return &a, nil
}

// accountArgs is the whole row, so an insert and an update name the same values.
func accountArgs(a *domain.Account) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id":                 a.ID,
		"version":            a.Version,
		"status":             string(a.Status),
		"role":               string(a.Role),
		"phone":              a.Phone,
		"email":              a.Email,
		"username":           a.Username,
		"password_hash":      a.PasswordHash,
		"email_verified":     a.EmailVerified,
		"suspended_until":    a.SuspendedUntil,
		"suspension_reason":  a.SuspensionReason,
		"name":               a.Profile.Name,
		"description":        a.Profile.Description,
		"gender":             a.Profile.Gender,
		"date_of_birth":      a.Profile.DateOfBirth,
		"avatar_resource_id": a.Profile.AvatarResourceID,
		"country":            a.Profile.Country,
		"locale":             a.Profile.Locale,
		"timezone":           a.Profile.Timezone,
	}
}

// profileColumns is the display half on its own, for a caller that wants a name and an
// avatar rather than an account.
const profileColumns = `id, name, description, gender::text, date_of_birth,
	       avatar_resource_id, country, locale, timezone`

func scanProfile(row pgx.Row) (domain.Profile, error) {
	var p domain.Profile
	var gender *string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &gender, &p.DateOfBirth,
		&p.AvatarResourceID, &p.Country, &p.Locale, &p.Timezone)
	p.Gender = (*domain.Gender)(gender)
	if dbx.IsNoRows(err) {
		return domain.Profile{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Profile{}, fmt.Errorf("db scan profile: %w", err)
	}
	return p, nil
}
