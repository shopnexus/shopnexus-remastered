package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/finance/domain"
)

const bankAccountColumns = `id, account_id, bank_code, account_number, account_holder,
	       is_default, created_at, deleted_at`

func scanBankAccount(row pgx.Row) (domain.BankAccount, error) {
	var b domain.BankAccount
	err := row.Scan(&b.ID, &b.AccountID, &b.BankCode, &b.AccountNumber, &b.AccountHolder,
		&b.IsDefault, &b.CreatedAt, &b.DeletedAt)
	if dbx.IsNoRows(err) {
		return domain.BankAccount{}, domain.ErrBankAccountNotFound
	}
	if err != nil {
		return domain.BankAccount{}, fmt.Errorf("db scan bank account: %w", err)
	}
	return b, nil
}

// InsertBankAccount writes the destination, clearing the previous default in the same
// transaction when this one claims it. The partial unique index enforces one default
// per account, so the clear is not an optimisation — without it the insert fails.
func (r *Repo) InsertBankAccount(ctx context.Context, b *domain.BankAccount) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if b.IsDefault {
			if err := clearDefaultBankAccount(ctx, tx, b.AccountID); err != nil {
				return err
			}
		}
		const q = `INSERT INTO bank_account
		             (account_id, bank_code, account_number, account_holder, is_default)
		           VALUES (@account_id, @bank_code, @account_number, @account_holder, @is_default)
		           RETURNING id, created_at`
		args := pgx.NamedArgs{
			"account_id": b.AccountID, "bank_code": b.BankCode,
			"account_number": b.AccountNumber, "account_holder": b.AccountHolder,
			"is_default": b.IsDefault,
		}
		if err := tx.QueryRow(ctx, q, args).Scan(&b.ID, &b.CreatedAt); err != nil {
			return fmt.Errorf("db insert bank account: %w", err)
		}
		return nil
	})
}

func (r *Repo) FindBankAccount(ctx context.Context, id, accountID int64) (domain.BankAccount, error) {
	const q = `SELECT ` + bankAccountColumns + ` FROM bank_account
	           WHERE id = @id AND account_id = @account_id AND deleted_at IS NULL`
	args := pgx.NamedArgs{"id": id, "account_id": accountID}
	return scanBankAccount(r.pool.QueryRow(ctx, q, args))
}

func (r *Repo) BankAccountsByIDs(ctx context.Context, ids []int64) (map[int64]domain.BankAccount, error) {
	out := make(map[int64]domain.BankAccount, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// No account_id and no deleted_at: see the port for why both omissions are the point.
	const q = `SELECT ` + bankAccountColumns + ` FROM bank_account WHERE id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": dbx.Int64Array(ids)})
	if err != nil {
		return nil, fmt.Errorf("db query bank accounts by ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		b, err := scanBankAccount(rows)
		if err != nil {
			return nil, err
		}
		out[b.ID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate bank accounts: %w", err)
	}
	return out, nil
}

func (r *Repo) ListBankAccounts(ctx context.Context, accountID int64) ([]domain.BankAccount, error) {
	const q = `SELECT ` + bankAccountColumns + ` FROM bank_account
	           WHERE account_id = @account_id AND deleted_at IS NULL
	           ORDER BY is_default DESC, created_at DESC`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query bank accounts: %w", err)
	}
	defer rows.Close()

	var out []domain.BankAccount
	for rows.Next() {
		b, err := scanBankAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate bank accounts: %w", err)
	}
	return out, nil
}

func (r *Repo) SaveBankAccount(ctx context.Context, b domain.BankAccount) error {
	return dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if b.IsDefault {
			if err := clearDefaultBankAccount(ctx, tx, b.AccountID); err != nil {
				return err
			}
		}
		const q = `UPDATE bank_account
		           SET bank_code = @bank_code, account_number = @account_number,
		               account_holder = @account_holder, is_default = @is_default
		           WHERE id = @id AND account_id = @account_id AND deleted_at IS NULL`
		args := pgx.NamedArgs{
			"id": b.ID, "account_id": b.AccountID, "bank_code": b.BankCode,
			"account_number": b.AccountNumber, "account_holder": b.AccountHolder,
			"is_default": b.IsDefault,
		}
		tag, err := tx.Exec(ctx, q, args)
		if err != nil {
			return fmt.Errorf("db update bank account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrBankAccountNotFound
		}
		return nil
	})
}

// SoftDeleteBankAccount refuses while a withdrawal to it is still in flight: the
// session names this row as where the money is going, and a payee that vanished
// mid-transfer is a payment nobody can trace.
func (r *Repo) SoftDeleteBankAccount(ctx context.Context, id, accountID int64) error {
	const q = `UPDATE bank_account SET deleted_at = now()
	           WHERE id = @id AND account_id = @account_id AND deleted_at IS NULL
	             AND NOT EXISTS (
	               SELECT 1 FROM payment_session
	               WHERE kind = 'withdrawal' AND status IN ('pending', 'processing')
	                 AND (data ->> 'bank_account_id')::bigint = @id
	             )`
	args := pgx.NamedArgs{"id": id, "account_id": accountID}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db soft delete bank account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either it is not there or a withdrawal still points at it. The caller reads it
		// first, so what is left is the one it can act on.
		return domain.ErrBankAccountInUse
	}
	return nil
}

// clearDefaultBankAccount is the same idea as account.contact's default: the partial
// unique index holds even when a service is wrong, so the clear runs in the same
// transaction as the write that claims the flag.
func clearDefaultBankAccount(ctx context.Context, tx pgx.Tx, accountID int64) error {
	const q = `UPDATE bank_account SET is_default = false
	           WHERE account_id = @account_id AND is_default AND deleted_at IS NULL`
	if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"account_id": accountID}); err != nil {
		return fmt.Errorf("db clear default bank account: %w", err)
	}
	return nil
}

const taxInfoColumns = `account_id, tax_code, tax_code_type, legal_name,
	       verification_status::text, verified_at, verification_source, created_at, updated_at`

func scanTaxInfo(row pgx.Row) (domain.TaxInfo, error) {
	var t domain.TaxInfo
	err := row.Scan(&t.AccountID, &t.TaxCode, &t.TaxCodeType, &t.LegalName,
		&t.VerificationStatus, &t.VerifiedAt, &t.VerificationSource, &t.CreatedAt, &t.UpdatedAt)
	if dbx.IsNoRows(err) {
		return domain.TaxInfo{}, domain.ErrTaxInfoNotFound
	}
	if err != nil {
		return domain.TaxInfo{}, fmt.Errorf("db scan tax info: %w", err)
	}
	return t, nil
}

// PutTaxInfo replaces the registration. Filing again resets the verdict, which is why
// this is an upsert on the account rather than an insert: there is one registration
// per account by design.
func (r *Repo) PutTaxInfo(ctx context.Context, t domain.TaxInfo) error {
	const q = `INSERT INTO tax_info
	             (account_id, tax_code, tax_code_type, legal_name, verification_status)
	           VALUES (@account_id, @tax_code, @tax_code_type, @legal_name, @verification_status)
	           ON CONFLICT (account_id) DO UPDATE SET
	             tax_code = @tax_code, tax_code_type = @tax_code_type,
	             legal_name = @legal_name, verification_status = @verification_status,
	             verified_at = NULL, verification_source = NULL, updated_at = now()`
	args := pgx.NamedArgs{
		"account_id": t.AccountID, "tax_code": t.TaxCode, "tax_code_type": t.TaxCodeType,
		"legal_name": t.LegalName, "verification_status": t.VerificationStatus,
	}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		// tax_info_tax_code_verified_uq: only one account may hold a verified code.
		if dbx.IsUniqueViolation(err) {
			return domain.ErrTaxCodeTaken
		}
		return fmt.Errorf("db upsert tax info: %w", err)
	}
	return nil
}

func (r *Repo) FindTaxInfo(ctx context.Context, accountID int64) (domain.TaxInfo, error) {
	const q = `SELECT ` + taxInfoColumns + ` FROM tax_info WHERE account_id = @account_id`
	return scanTaxInfo(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}))
}

// SaveTaxInfo records a verdict. `WHERE verification_status = 'pending'` is the
// transition, so two moderators deciding at once cannot both land.
func (r *Repo) SaveTaxInfo(ctx context.Context, t domain.TaxInfo) error {
	const q = `UPDATE tax_info
	           SET verification_status = @status, verified_at = @verified_at,
	               verification_source = @source, updated_at = now()
	           WHERE account_id = @account_id AND verification_status = 'pending'`
	args := pgx.NamedArgs{
		"account_id": t.AccountID, "status": t.VerificationStatus,
		"verified_at": t.VerifiedAt, "source": t.VerificationSource,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		if dbx.IsUniqueViolation(err) {
			return domain.ErrTaxCodeTaken
		}
		return fmt.Errorf("db update tax info: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTaxInfoSettled
	}
	return nil
}
