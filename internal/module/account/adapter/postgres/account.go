package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
)

// CreateAccount writes the account and its profile together. Two statements, one
// transaction: the profile shares the account's primary key, so it cannot be
// inserted first, and an account with no display name must never be visible.
func (r *Repo) CreateAccount(ctx context.Context, a *domain.Account, p *domain.Profile) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		const insertAccount = `INSERT INTO account (status, role, phone, email, username, password)
		           VALUES (@status, @role, @phone, @email, @username, @password)
		           RETURNING id, created_at`
		args := pgx.NamedArgs{
			"status":   string(a.Status),
			"role":     string(a.Role),
			"phone":    nullText(a.Phone),
			"email":    nullText(a.Email),
			"username": nullText(a.Username),
			"password": nullText(a.PasswordHash),
		}
		if err := tx.QueryRow(ctx, insertAccount, args).Scan(&a.ID, &a.CreatedAt); err != nil {
			if isUniqueViolation(err) {
				return domain.ErrIdentifierTaken
			}
			return fmt.Errorf("db insert account: %w", err)
		}

		const insertProfile = `INSERT INTO profile (id, name, description, gender, date_of_birth,
		                                avatar_resource_id, country, locale, timezone)
		           VALUES (@id, @name, @description, @gender, @date_of_birth,
		                   @avatar_resource_id, @country, @locale, @timezone)
		           RETURNING created_at`
		p.ID = a.ID
		pargs := pgx.NamedArgs{
			"id":                 p.ID,
			"name":               p.Name,
			"description":        nullText(p.Description),
			"gender":             nullText(string(p.Gender)),
			"date_of_birth":      p.DateOfBirth,
			"avatar_resource_id": nullID(p.AvatarResourceID),
			"country":            p.Country,
			"locale":             p.Locale,
			"timezone":           p.Timezone,
		}
		if err := tx.QueryRow(ctx, insertProfile, pargs).Scan(&p.CreatedAt); err != nil {
			return fmt.Errorf("db insert profile: %w", err)
		}
		return nil
	})
}

func (r *Repo) FindAccountByID(ctx context.Context, id int64) (domain.Account, error) {
	q := `SELECT ` + accountColumns + ` FROM account WHERE id = @id`
	return scanAccount(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": id}))
}

// FindAccountByIdentifier is the sign-in lookup. All three identifiers are UNIQUE, so
// this is a key lookup whichever one the caller sent, and matching all three in one
// statement is what lets the API keep quiet about which kind it was.
func (r *Repo) FindAccountByIdentifier(ctx context.Context, identifier string) (domain.Account, error) {
	q := `SELECT ` + accountColumns + `
	      FROM account
	      WHERE email = @identifier OR phone = @identifier OR username = @identifier`
	return scanAccount(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"identifier": identifier}))
}

func (r *Repo) FindAccountByEmail(ctx context.Context, email string) (domain.Account, error) {
	q := `SELECT ` + accountColumns + ` FROM account WHERE email = @email`
	return scanAccount(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"email": email}))
}

// UpdateAccountIdentifiers writes all three at once together with email_verified,
// because clearing that flag is part of changing the address and the two must not be
// separable.
func (r *Repo) UpdateAccountIdentifiers(ctx context.Context, a domain.Account) error {
	const q = `UPDATE account
	           SET phone = @phone, email = @email, username = @username, email_verified = @email_verified
	           WHERE id = @id`
	args := pgx.NamedArgs{
		"id":             a.ID,
		"phone":          nullText(a.Phone),
		"email":          nullText(a.Email),
		"username":       nullText(a.Username),
		"email_verified": a.EmailVerified,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrIdentifierTaken
		}
		return fmt.Errorf("db update account identifiers: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

func (r *Repo) UpdateAccountPassword(ctx context.Context, accountID int64, passwordHash string) error {
	const q = `UPDATE account SET password = @password WHERE id = @id`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": accountID, "password": passwordHash})
	if err != nil {
		return fmt.Errorf("db update account password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

func (r *Repo) MarkEmailVerified(ctx context.Context, accountID int64) error {
	const q = `UPDATE account SET email_verified = true WHERE id = @id`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": accountID})
	if err != nil {
		return fmt.Errorf("db mark email verified: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

// UpdateAccountStatus writes the suspension state as one unit — the column CHECK
// requires the details to be present only while suspended.
func (r *Repo) UpdateAccountStatus(ctx context.Context, a domain.Account) error {
	const q = `UPDATE account
	           SET status = @status, suspended_until = @suspended_until, suspension_reason = @suspension_reason
	           WHERE id = @id`
	args := pgx.NamedArgs{
		"id":                a.ID,
		"status":            string(a.Status),
		"suspended_until":   a.SuspendedUntil,
		"suspension_reason": nullText(a.SuspensionReason),
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update account status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

func (r *Repo) UpdateAccountRole(ctx context.Context, accountID int64, role domain.Role) error {
	const q = `UPDATE account SET role = @role WHERE id = @id`
	tag, err := r.pool.Exec(ctx, q, pgx.NamedArgs{"id": accountID, "role": string(role)})
	if err != nil {
		return fmt.Errorf("db update account role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

// SearchAccounts is the moderator's search. One statement covers both shapes of
// query: an exact hit on any identifier (a key lookup, which is how a moderator
// arrives here from a report) or a fragment of the display name (served by the
// trigram index on profile.name).
//
// COUNT(*) OVER () brings the total back with the rows, so a page costs one round
// trip rather than a query plus a matching count that can drift from it.
func (r *Repo) SearchAccounts(ctx context.Context, f port.AccountFilter) ([]domain.Account, int64, error) {
	const q = `SELECT a.id, a.status::text, a.role::text, COALESCE(a.phone, ''), COALESCE(a.email, ''),
	                  COALESCE(a.username, ''), COALESCE(a.password, ''), a.email_verified, a.created_at,
	                  a.suspended_until, COALESCE(a.suspension_reason, ''), COUNT(*) OVER () AS total_count
	           FROM account a
	           JOIN profile p ON p.id = a.id
	           WHERE (@query = '' OR a.email = @query OR a.phone = @query OR a.username = @query
	                  OR p.name ILIKE '%' || @query || '%')
	             AND (@status = '' OR a.status::text = @status)
	             AND (@role = '' OR a.role::text = @role)
	           ORDER BY a.created_at DESC, a.id DESC
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"query":  f.Query,
		"status": string(f.Status),
		"role":   string(f.Role),
		"limit":  f.Limit,
		"offset": f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query accounts: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Account
		total int64
	)
	for rows.Next() {
		var a domain.Account
		if err := rows.Scan(&a.ID, &a.Status, &a.Role, &a.Phone, &a.Email, &a.Username,
			&a.PasswordHash, &a.EmailVerified, &a.CreatedAt, &a.SuspendedUntil,
			&a.SuspensionReason, &total); err != nil {
			return nil, 0, fmt.Errorf("db scan account row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate accounts: %w", err)
	}
	return out, total, nil
}

// --- profile ---

func (r *Repo) FindProfile(ctx context.Context, accountID int64) (domain.Profile, error) {
	q := `SELECT ` + profileColumns + ` FROM profile WHERE id = @id`
	return scanProfile(r.pool.QueryRow(ctx, q, pgx.NamedArgs{"id": accountID}))
}

// FindProfiles resolves a batch, so a page of twenty followers costs one query rather
// than twenty.
func (r *Repo) FindProfiles(ctx context.Context, accountIDs []int64) (map[int64]domain.Profile, error) {
	out := make(map[int64]domain.Profile, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	q := `SELECT ` + profileColumns + ` FROM profile WHERE id = ANY(@ids)`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": accountIDs})
	if err != nil {
		return nil, fmt.Errorf("db query profiles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p domain.Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Gender, &p.DateOfBirth,
			&p.AvatarResourceID, &p.Country, &p.Locale, &p.Timezone, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("db scan profile row: %w", err)
		}
		out[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate profiles: %w", err)
	}
	return out, nil
}

func (r *Repo) UpdateProfile(ctx context.Context, p domain.Profile) error {
	const q = `UPDATE profile
	           SET name = @name, description = @description, gender = @gender,
	               date_of_birth = @date_of_birth, avatar_resource_id = @avatar_resource_id,
	               country = @country, locale = @locale, timezone = @timezone
	           WHERE id = @id`
	args := pgx.NamedArgs{
		"id":                 p.ID,
		"name":               p.Name,
		"description":        nullText(p.Description),
		"gender":             nullText(string(p.Gender)),
		"date_of_birth":      p.DateOfBirth,
		"avatar_resource_id": nullID(p.AvatarResourceID),
		"country":            p.Country,
		"locale":             p.Locale,
		"timezone":           p.Timezone,
	}
	tag, err := r.pool.Exec(ctx, q, args)
	if err != nil {
		return fmt.Errorf("db update profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}

// --- the cross-table facts an account view needs ---

// HasLiveVerifiedDocument is the payout gate's question: verified *and* not past its
// own expiry. The partial unique index on (account_id) WHERE status = 'verified'
// answers it without a scan.
func (r *Repo) HasLiveVerifiedDocument(ctx context.Context, accountID int64) (bool, error) {
	const q = `SELECT EXISTS (
	               SELECT 1 FROM identity_document
	               WHERE account_id = @account_id AND status = 'verified'
	                 AND (expires_at IS NULL OR expires_at > now())
	           )`
	var ok bool
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&ok); err != nil {
		return false, fmt.Errorf("db query live verified document: %w", err)
	}
	return ok, nil
}

func (r *Repo) LiveVerifiedDocuments(ctx context.Context, accountIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(accountIDs))
	if len(accountIDs) == 0 {
		return out, nil
	}
	const q = `SELECT account_id FROM identity_document
	           WHERE account_id = ANY(@ids) AND status = 'verified'
	             AND (expires_at IS NULL OR expires_at > now())`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"ids": accountIDs})
	if err != nil {
		return nil, fmt.Errorf("db query live verified documents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db scan live verified document row: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate live verified documents: %w", err)
	}
	return out, nil
}

func (r *Repo) CountFollowers(ctx context.Context, accountID int64) (int64, error) {
	const q = `SELECT COUNT(*) FROM follow WHERE followee_id = @account_id`
	var n int64
	if err := r.pool.QueryRow(ctx, q, pgx.NamedArgs{"account_id": accountID}).Scan(&n); err != nil {
		return 0, fmt.Errorf("db count followers: %w", err)
	}
	return n, nil
}

// --- audit ---

// InsertAuditLog appends the next version for that record in one statement: the
// version is derived from the rows already there, so two concurrent writers collide on
// the UNIQUE (table_name, record_id, version) instead of silently overwriting a
// history entry.
func (r *Repo) InsertAuditLog(ctx context.Context, e port.AuditEntry) error {
	const q = `INSERT INTO audit_log (version, table_name, record_id, change_type, code, changed_by, diff, snapshot)
	           SELECT COALESCE(MAX(version), 0) + 1, @table_name, @record_id, @change_type, @code,
	                  @changed_by, @diff, @snapshot
	           FROM audit_log
	           WHERE table_name = @table_name AND record_id = @record_id`
	args := pgx.NamedArgs{
		"table_name":  e.Table,
		"record_id":   e.RecordID,
		"change_type": e.ChangeType,
		"code":        e.Code,
		"changed_by":  nullID(e.ChangedBy),
		"diff":        jsonObject(e.Diff),
		"snapshot":    jsonObject(e.Snapshot),
	}
	if _, err := r.pool.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db insert audit log: %w", err)
	}
	return nil
}
