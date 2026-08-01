package postgres

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"shopnexus/internal/module/common/dbx"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
)

const walletColumns = `account_id, currency, available_balance, held_balance, created_at`

func scanWallet(row pgx.Row) (domain.Wallet, error) {
	var w domain.Wallet
	err := row.Scan(&w.AccountID, &w.Currency, &w.AvailableBalance, &w.HeldBalance, &w.CreatedAt)
	if dbx.IsNoRows(err) {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	if err != nil {
		return domain.Wallet{}, fmt.Errorf("db scan wallet: %w", err)
	}
	return w, nil
}

func (r *Repo) ListWallets(ctx context.Context, accountID int64) ([]domain.Wallet, error) {
	const q = `SELECT ` + walletColumns + ` FROM wallet WHERE account_id = @account_id
	           ORDER BY currency`
	rows, err := r.pool.Query(ctx, q, pgx.NamedArgs{"account_id": accountID})
	if err != nil {
		return nil, fmt.Errorf("db query wallets: %w", err)
	}
	defer rows.Close()

	var out []domain.Wallet
	for rows.Next() {
		w, err := scanWallet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate wallets: %w", err)
	}
	return out, nil
}

// ListMovements pages one wallet's ledger newest first, which is the reverse of the
// sequence — and wallet_transaction_wallet_seq_key already orders those columns, so
// the scan runs backwards over it rather than sorting.
func (r *Repo) ListMovements(ctx context.Context, f port.MovementFilter) ([]domain.Movement, int64, error) {
	const q = `SELECT id, account_id, currency, seq, kind::text, available_delta, held_delta,
	                  available_after, held_after, group_id, ref_type, ref_id,
	                  idempotency_key, note, created_at, COUNT(*) OVER () AS total_count
	           FROM wallet_transaction
	           WHERE account_id = @account_id AND currency = @currency
	             AND (@kind::text IS NULL OR kind::text = @kind::text)
	           ORDER BY seq DESC
	           LIMIT @limit OFFSET @offset`
	args := pgx.NamedArgs{
		"account_id": f.AccountID, "currency": f.Currency, "kind": nullString(f.Kind),
		"limit": f.Limit, "offset": f.Offset,
	}
	rows, err := r.pool.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("db query movements: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Movement
		total int64
	)
	for rows.Next() {
		var m domain.Movement
		if err := rows.Scan(&m.ID, &m.AccountID, &m.Currency, &m.Seq, &m.Kind,
			&m.AvailableDelta, &m.HeldDelta, &m.AvailableAfter, &m.HeldAfter,
			&m.GroupID, &m.RefType, &m.RefID, &m.IdempotencyKey, &m.Note, &m.CreatedAt,
			&total); err != nil {
			return nil, 0, fmt.Errorf("db scan movement: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("db iterate movements: %w", err)
	}
	return out, total, nil
}

// Move is the only write that touches a balance. Every leg lands in one transaction,
// because the halves of a movement — a buyer's debit and the escrow hold against it —
// are one fact: a partial application is money that exists twice or not at all.
//
// Each wallet is taken with SELECT ... FOR UPDATE, which is what makes the sequence
// safe: two concurrent movements on one wallet would otherwise read the same MAX(seq)
// and collide on the unique index, and the second would fail after the first had
// already changed the balance.
func (r *Repo) Move(ctx context.Context, legs []port.Leg) ([]domain.Movement, error) {
	if len(legs) == 0 {
		return nil, nil
	}
	var out []domain.Movement
	err := dbx.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = move(ctx, tx, legs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// move is Move's body, so a caller that already has a transaction — opening a withdrawal,
// where the debit and the request are one fact — gets the same arithmetic rather than a
// second copy of it.
//
// The legs are locked in a fixed order (account, then currency) rather than the order the
// caller listed them. Two movements naming the same pair of wallets in opposite orders — a
// hold on one order while a refund on another runs the other way — would otherwise each hold
// one row and wait for the other, and Postgres would abort one as a deadlock.
func move(ctx context.Context, tx pgx.Tx, legs []port.Leg) ([]domain.Movement, error) {
	// One group id for the whole call, so the legs of a checkout can be pulled back
	// out together. Drawn from a sequence the schema owns rather than invented here.
	groupID, err := nextGroupID(ctx, tx)
	if err != nil {
		return nil, err
	}
	ordered := make([]port.Leg, len(legs))
	copy(ordered, legs)
	slices.SortStableFunc(ordered, func(a, b port.Leg) int {
		if a.AccountID != b.AccountID {
			return cmp.Compare(a.AccountID, b.AccountID)
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	out := make([]domain.Movement, 0, len(ordered))
	for _, leg := range ordered {
		w, err := lockWallet(ctx, tx, leg.AccountID, leg.Currency)
		if err != nil {
			return nil, err
		}
		seq, err := nextSeq(ctx, tx, leg.AccountID, leg.Currency)
		if err != nil {
			return nil, err
		}
		transfer := leg.Transfer
		if transfer.GroupID == nil {
			transfer.GroupID = &groupID
		}
		m, err := w.Apply(transfer, seq)
		if err != nil {
			return nil, err
		}
		if err := insertMovement(ctx, tx, m); err != nil {
			return nil, err
		}
		if err := saveBalances(ctx, tx, w); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// lockWallet reads the row for update, opening it at zero if the account has never
// held this currency. A wallet is not a thing anybody registers: it exists the first
// time money arrives.
func lockWallet(ctx context.Context, tx pgx.Tx, accountID int64, currency string) (domain.Wallet, error) {
	const open = `INSERT INTO wallet (account_id, currency) VALUES (@account_id, @currency)
	              ON CONFLICT (account_id, currency) DO NOTHING`
	args := pgx.NamedArgs{"account_id": accountID, "currency": currency}
	if _, err := tx.Exec(ctx, open, args); err != nil {
		return domain.Wallet{}, fmt.Errorf("db open wallet: %w", err)
	}
	const q = `SELECT ` + walletColumns + ` FROM wallet
	           WHERE account_id = @account_id AND currency = @currency
	           FOR UPDATE`
	return scanWallet(tx.QueryRow(ctx, q, args))
}

// nextSeq is the wallet's next ledger position. Read under the row lock the caller
// already holds, so it cannot be handed out twice.
func nextSeq(ctx context.Context, tx pgx.Tx, accountID int64, currency string) (int64, error) {
	const q = `SELECT COALESCE(MAX(seq), 0) + 1 FROM wallet_transaction
	           WHERE account_id = @account_id AND currency = @currency`
	var seq int64
	args := pgx.NamedArgs{"account_id": accountID, "currency": currency}
	if err := tx.QueryRow(ctx, q, args).Scan(&seq); err != nil {
		return 0, fmt.Errorf("db read next ledger seq: %w", err)
	}
	return seq, nil
}

// nextGroupID draws from the ledger's own id sequence. Any monotonic number would
// do; using the table's means no second sequence to create and migrate.
func nextGroupID(ctx context.Context, tx pgx.Tx) (int64, error) {
	const q = `SELECT nextval(pg_get_serial_sequence('wallet_transaction', 'id'))`
	var n int64
	if err := tx.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("db next movement group: %w", err)
	}
	return n, nil
}

func insertMovement(ctx context.Context, tx pgx.Tx, m domain.Movement) error {
	const q = `INSERT INTO wallet_transaction
	             (account_id, currency, seq, kind, available_delta, held_delta,
	              available_after, held_after, group_id, ref_type, ref_id,
	              idempotency_key, note)
	           VALUES (@account_id, @currency, @seq, @kind, @available_delta, @held_delta,
	                   @available_after, @held_after, @group_id, @ref_type, @ref_id,
	                   @idempotency_key, @note)`
	args := pgx.NamedArgs{
		"account_id": m.AccountID, "currency": m.Currency, "seq": m.Seq, "kind": m.Kind,
		"available_delta": m.AvailableDelta, "held_delta": m.HeldDelta,
		"available_after": m.AvailableAfter, "held_after": m.HeldAfter,
		"group_id": m.GroupID, "ref_type": m.RefType, "ref_id": m.RefID,
		"idempotency_key": m.IdempotencyKey, "note": m.Note,
	}
	if _, err := tx.Exec(ctx, q, args); err != nil {
		// The unique index on the key is what makes a retried movement safe: the second
		// attempt loses here rather than posting the money twice.
		if dbx.IsUniqueViolation(err) {
			return domain.ErrMovementAlreadyPosted
		}
		return fmt.Errorf("db insert movement: %w", err)
	}
	return nil
}

func saveBalances(ctx context.Context, tx pgx.Tx, w domain.Wallet) error {
	const q = `UPDATE wallet SET available_balance = @available, held_balance = @held
	           WHERE account_id = @account_id AND currency = @currency`
	args := pgx.NamedArgs{
		"available": w.AvailableBalance, "held": w.HeldBalance,
		"account_id": w.AccountID, "currency": w.Currency,
	}
	if _, err := tx.Exec(ctx, q, args); err != nil {
		return fmt.Errorf("db update wallet balances: %w", err)
	}
	return nil
}
