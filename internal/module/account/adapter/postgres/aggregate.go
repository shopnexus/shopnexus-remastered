package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/common"
	"shopnexus/internal/module/common/dbx"
)

// querier is what a pool and a transaction have in common, so the aggregate loads the
// same way inside a transaction as outside one.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Create writes the account, display half included — one row, so an account with no
// display name is not a reachable state.
func (r *Repo) Create(ctx context.Context, a *domain.Account) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `INSERT INTO account (status, role, phone, email, username, password_hash,
		                        email_verified, name, description, gender, date_of_birth,
		                        avatar_resource_id, country, locale, timezone)
		           VALUES (@status, @role, @phone, @email, @username, @password_hash,
		                   @email_verified, @name, @description, @gender, @date_of_birth,
		                   @avatar_resource_id, @country, @locale, @timezone)
		           RETURNING id, version, created_at`
		if err := tx.QueryRow(ctx, q, accountArgs(a)).Scan(&a.ID, &a.Version, &a.CreatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrIdentifierTaken
			}
			return fmt.Errorf("db insert account: %w", err)
		}
		a.Profile.ID = a.ID
		// A federated sign-in creates and links in one command, so the child is written here.
		if err := saveIdentities(ctx, tx, a); err != nil {
			return err
		}
		a.ClearEvents()
		return nil
	})
}

func (r *Repo) Get(ctx context.Context, id int64) (*domain.Account, error) {
	return loadAggregate(ctx, r.pool, `WHERE id = @key`, pgx.NamedArgs{"key": id})
}

// GetByIdentifier is the sign-in lookup. All three identifiers are UNIQUE, so this is a
// key lookup whichever one the caller sent, and matching all three in one statement is
// what lets the API keep quiet about which kind it was.
func (r *Repo) GetByIdentifier(ctx context.Context, identifier string) (*domain.Account, error) {
	return loadAggregate(ctx, r.pool,
		`WHERE email = @key OR phone = @key OR username = @key`,
		pgx.NamedArgs{"key": identifier})
}

func (r *Repo) GetByEmail(ctx context.Context, email string) (*domain.Account, error) {
	return loadAggregate(ctx, r.pool, `WHERE email = @key`, pgx.NamedArgs{"key": email})
}

// GetByOAuth resolves a provider's subject straight to the account, so a federated
// sign-in is one round trip rather than a link lookup followed by an account read. A
// subject nobody has linked is ErrOAuthIdentityNotFound, which is the answer the caller
// branches on.
func (r *Repo) GetByOAuth(ctx context.Context, provider, providerUID string) (*domain.Account, error) {
	const q = `SELECT account_id FROM oauth_identity
	           WHERE provider = @provider AND provider_uid = @provider_uid`
	var accountID int64
	err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"provider": provider, "provider_uid": providerUID}).
		Scan(&accountID)
	if err != nil {
		if dbx.IsNoRows(err) {
			return nil, domain.ErrOAuthIdentityNotFound
		}
		return nil, fmt.Errorf("db query oauth identity: %w", err)
	}
	return r.Get(ctx, accountID)
}

// Save is the aggregate's only write. The version check serialises two concurrent
// commands: the loser rewrites nothing and is told so.
func (r *Repo) Save(ctx context.Context, a *domain.Account, actor int64) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const q = `UPDATE account
		           SET status = @status, role = @role, phone = @phone, email = @email,
		               username = @username, password_hash = @password_hash,
		               email_verified = @email_verified, suspended_until = @suspended_until,
		               suspension_reason = @suspension_reason, name = @name,
		               description = @description, gender = @gender,
		               date_of_birth = @date_of_birth, avatar_resource_id = @avatar_resource_id,
		               country = @country, locale = @locale, timezone = @timezone,
		               version = version + 1
		           WHERE id = @id AND version = @version`
		tag, err := tx.Exec(ctx, q, accountArgs(a))
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrIdentifierTaken
			}
			return fmt.Errorf("db update account: %w", err)
		}
		// Zero rows is either a stale version or a missing account. The distinction costs a
		// second query and changes nothing a caller does, so the conflict is the answer.
		if tag.RowsAffected() == 0 {
			return domain.ErrVersionConflict
		}
		if err := saveIdentities(ctx, tx, a); err != nil {
			return err
		}
		// Bumped before the trail is written, so the snapshot carries the version the row
		// now has rather than the one it was read at.
		a.Version++
		if err := saveEvents(ctx, tx, a, actor); err != nil {
			return err
		}
		a.ClearEvents()
		return nil
	})
}

// loadAggregate reads the root and its links: two keyed queries for every command, which
// is the price of there being exactly one way in.
func loadAggregate(ctx context.Context, q querier, where string, args pgx.NamedArgs) (*domain.Account, error) {
	a, err := scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM account `+where, args))
	if err != nil {
		return nil, err
	}
	a.Identities, err = listIdentities(ctx, q, a.ID)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// saveIdentities makes the table match the slice: delete what left it, insert what has no
// id yet. No removal list to keep, which is what lets the field stay exported — safe only
// because Get is the single loader, so the slice is always the whole set.
func saveIdentities(ctx context.Context, tx pgx.Tx, a *domain.Account) error {
	keep := make([]string, 0, len(a.Identities))
	for _, i := range a.Identities {
		keep = append(keep, i.Provider)
	}
	const del = `DELETE FROM oauth_identity WHERE account_id = @account_id AND provider <> ALL(@keep)`
	if _, err := tx.Exec(ctx, del, pgx.NamedArgs{"account_id": a.ID, "keep": keep}); err != nil {
		return fmt.Errorf("db delete oauth identities: %w", err)
	}
	const ins = `INSERT INTO oauth_identity (account_id, provider, provider_uid)
	             VALUES (@account_id, @provider, @provider_uid)
	             RETURNING id, created_at`
	for _, i := range a.Identities {
		if i.ID != 0 {
			continue
		}
		i.AccountID = a.ID
		args := pgx.NamedArgs{"account_id": a.ID, "provider": i.Provider, "provider_uid": i.ProviderUID}
		if err := tx.QueryRow(ctx, ins, args).Scan(&i.ID, &i.CreatedAt); err != nil {
			if dbx.IsUniqueViolation(err) {
				return domain.ErrIdentifierTaken
			}
			return fmt.Errorf("db insert oauth identity: %w", err)
		}
	}
	return nil
}

// saveEvents writes the trail in the same transaction as the change it describes: a write
// that landed always has one, and the diff comes from the decision rather than from a
// reconstruction after the fact.
func saveEvents(ctx context.Context, tx pgx.Tx, a *domain.Account, actor int64) error {
	events := a.Events()
	if len(events) == 0 {
		return nil
	}
	// Zero means no account is responsible, which the column spells NULL.
	var changedBy *int64
	if actor != 0 {
		changedBy = &actor
	}
	snapshot := a.Snapshot()
	for _, e := range events {
		entry := common.AuditEntry{
			Table:      "account",
			RecordID:   a.ID,
			ChangeType: common.ChangeTypeUpdate,
			Code:       string(e.Code),
			ChangedBy:  changedBy,
			Diff:       e.Payload,
			Snapshot:   snapshot,
		}
		if err := dbx.InsertAuditLog(ctx, tx, entry); err != nil {
			return err
		}
	}
	return nil
}
