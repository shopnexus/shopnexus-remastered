package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
)

// The money, which the old seeder left out entirely: an order had no payment session, no escrow
// movement and no wallet entry, so every read-only screen worked and every screen about money
// was blank. A seller's earnings page showing a zero balance under a shop with forty completed
// sales is not a screenshot a report can use.
//
// What is written here mirrors what the service does, movement for movement, because the
// idempotency keys are how a later real action finds out the movement already happened. A
// refund accepted through the UI on a seeded order posts `order:<id>:refund:seller`; if the
// seed used a different key the money would move twice.

// amounts is the two numbers every order is settled on. They are computed once, before
// anything is written, because finance needs them to size the payment session and order needs
// them for the line — and the two must not disagree.
type amounts struct {
	total int64 // goods, which is what the escrow holds and the payout releases
	fee   int64 // delivery, which the buyer pays and the seller never receives
}

func computeAmounts(p *plan, parties map[string]party) (map[string]amounts, error) {
	out := make(map[string]amounts, len(p.orders))
	for _, o := range p.orders {
		buyer, ok := parties[o.buyer]
		if !ok {
			return nil, fmt.Errorf("order %s: no such buyer %q", o.key, o.buyer)
		}
		seller, ok := parties[o.seller]
		if !ok {
			return nil, fmt.Errorf("order %s: no such seller %q", o.key, o.seller)
		}
		total := p.listings[o.listing].variants[o.variant].price * o.quantity
		if o.offerKey != "" {
			of, ok := p.offer(o.offerKey)
			if !ok {
				return nil, fmt.Errorf("order %s: no such offer %q", o.key, o.offerKey)
			}
			total = of.total
		}
		out[o.key] = amounts{
			total: total,
			fee:   deliveryFee(buyer.area.provinceCode == seller.area.provinceCode),
		}
	}
	return out, nil
}

// writePaymentSessions writes the buyer's checkout for every order, before the order itself:
// "item"."payment_session_id" is NOT NULL and the webhook path groups a checkout's lines by it,
// so a shared zero would make every seeded line one enormous session.
func writePaymentSessions(
	ctx context.Context, pool *pgxpool.Pool, p *plan,
	parties map[string]party, amt map[string]amounts,
) (map[string]int64, error) {
	out := make(map[string]int64, len(p.orders))
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, o := range p.orders {
			a := amt[o.key]
			const q = `
				INSERT INTO payment_session (kind, status, from_id, to_id, note, currency,
				                             total_amount, data, created_at, paid_at, expired_at)
				VALUES ('buyer-checkout', 'success', @from_id, @to_id, @note, @currency,
				        @total_amount, @data, @created_at, @paid_at, @expired_at)
				RETURNING id`
			var id int64
			err := tx.QueryRow(ctx, q, pgx.NamedArgs{
				"from_id":  parties[o.buyer].id,
				"to_id":    parties[o.seller].id,
				"note":     "Thanh toán đơn hàng " + p.listings[o.listing].name,
				"currency": currency,
				// The session collected the goods and the delivery: the buyer pays both, and
				// only the first of them is ever the seller's.
				"total_amount": a.total + a.fee,
				"data": map[string]any{
					"shipping_fee": a.fee,
					"source":       "seed",
				},
				"created_at": o.createdAt,
				"paid_at":    o.createdAt.Add(4 * time.Minute),
				"expired_at": o.createdAt.Add(time.Hour),
			}).Scan(&id)
			if err != nil {
				return fmt.Errorf("insert payment session for %s: %w", o.key, err)
			}
			out[o.key] = id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// move is one leg of one balance change, before the running balances are worked out.
type move struct {
	account        string // account key
	kind           string // wallet_txn_kind
	availableDelta int64
	heldDelta      int64
	refType        string // 'order' | 'payment-session' | ""
	refID          int64
	idemKey        string
	note           string
	at             time.Time
	group          int64
}

// openingBalance is what the three demo accounts start with, so a wallet page is not a column
// of zeroes before the first sale. A top-up is a real movement kind and this is exactly what
// one looks like.
const openingBalance = 3_000_000

// writeLedger lands the wallets and their ledgers. Everything is worked out in memory first,
// in time order, because "available_after" is a running balance: a row that disagrees with the
// one before it is a ledger that does not add up, and the schema will not catch it.
func writeLedger(
	ctx context.Context, pool *pgxpool.Pool, p *plan,
	parties map[string]party, amt map[string]amounts, sales salesResult, sessions map[string]int64,
) (int, error) {
	var moves []move
	var group int64

	next := func() int64 { group++; return group }

	// Opening top-ups, oldest thing in the ledger.
	openedAt := p.now.Add(-catalogueAge)
	for _, key := range []string{buyerKey, shopKey, "huyen_camera", "tuan_sport", "linh_home"} {
		g := next()
		moves = append(moves, move{
			account: key, kind: "topup", availableDelta: openingBalance,
			idemKey: "seed:opening:" + key, note: "Nạp tiền vào ví",
			at: openedAt, group: g,
		})
	}

	for _, o := range p.orders {
		a := amt[o.key]
		orderID := sales.orderIDs[o.key]
		sessionID := sessions[o.key]
		tl := timelineFor(o)

		// The buyer's money arrives on a rail and becomes a balance, then the balance is what
		// the checkout debits. Two movements, because the rail leg and the wallet leg are
		// different ledgers and the same money must not be booked in both.
		g := next()
		moves = append(moves, move{
			account: o.buyer, kind: "topup", availableDelta: a.total + a.fee,
			refType: "payment-session", refID: sessionID,
			idemKey: fmt.Sprintf("session:%d:topup", sessionID),
			note:    "Thanh toán đơn hàng",
			at:      o.createdAt, group: g,
		})

		g = next()
		hold := fmt.Sprintf("order:%d:hold", orderID)
		moves = append(moves,
			move{
				account: o.buyer, kind: "escrow-hold", availableDelta: -a.total,
				refType: "order", refID: orderID, idemKey: hold + ":buyer",
				note: "checkout debit", at: o.createdAt.Add(time.Minute), group: g,
			},
			move{
				account: o.seller, kind: "escrow-hold", heldDelta: a.total,
				refType: "order", refID: orderID, idemKey: hold + ":seller",
				note: "escrow hold", at: o.createdAt.Add(time.Minute), group: g,
			},
			move{
				account: o.buyer, kind: "fee", availableDelta: -a.fee,
				refType: "order", refID: orderID, idemKey: hold + ":shipping",
				note: "shipping fee", at: o.createdAt.Add(time.Minute), group: g,
			},
		)

		switch o.state {
		case stateCompleted:
			g = next()
			moves = append(moves, move{
				account: o.seller, kind: "escrow-release",
				availableDelta: a.total, heldDelta: -a.total,
				refType: "order", refID: orderID,
				idemKey: fmt.Sprintf("order:%d:release", orderID),
				note:    "escrow release", at: *tl.payoutAt, group: g,
			})
		case stateDeclined, stateRefundAccepted:
			// The money goes back. The delivery fee comes back only on a cancellation — on a
			// settled refund the parcel really did travel, and the carrier was really paid.
			at := *tl.cancelledAt
			g = next()
			refund := fmt.Sprintf("order:%d:refund", orderID)
			moves = append(moves,
				move{
					account: o.seller, kind: "refund", heldDelta: -a.total,
					refType: "order", refID: orderID, idemKey: refund + ":seller",
					note: "refund released from escrow", at: at, group: g,
				},
				move{
					account: o.buyer, kind: "refund", availableDelta: a.total,
					refType: "order", refID: orderID, idemKey: refund + ":buyer",
					note: "refund returned", at: at, group: g,
				},
			)
			if o.state == stateDeclined {
				moves = append(moves, move{
					account: o.buyer, kind: "refund", availableDelta: a.fee,
					refType: "order", refID: orderID, idemKey: refund + ":shipping",
					note: "shipping fee returned", at: at, group: g,
				})
			}
		}
	}

	// Sort by time, keeping the order legs were listed in when two share an instant: the
	// top-up has to land before the debit it pays for, or the running balance goes negative.
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].at.Before(moves[j].at) })

	// Two cash-outs for the shop, so the withdrawal tab and the admin queue are not empty:
	// one paid, one still waiting on a human. Sized from what the shop has actually earned
	// rather than from a constant — a wish of five million against a wallet holding three is
	// a ledger that goes negative, which is a failed seed rather than a wrong number.
	withdrawals := []struct {
		share  int64 // percent of what is spendable from that moment on
		status string
		at     time.Time
	}{
		{share: 30, status: "success", at: p.now.Add(-20 * 24 * time.Hour)},
		{share: 20, status: "pending", at: p.now.Add(-16 * time.Hour)},
	}

	written := 0
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		bankID, err := writeBankAccount(ctx, tx, parties[shopKey].id, p.now.Add(-catalogueAge))
		if err != nil {
			return err
		}
		for _, w := range withdrawals {
			// A withdrawal only ever subtracts, so what bounds it is the *lowest* the balance
			// gets between that moment and the end of the ledger, not the balance on the day.
			amount := roundDown(minAvailableFrom(moves, shopKey, w.at)*w.share/100, 500_000)
			if amount <= 0 {
				continue
			}
			sessionID, err := writeWithdrawalSession(ctx, tx, parties[shopKey].id, bankID, amount, w.status, w.at)
			if err != nil {
				return err
			}
			group++
			moves = append(moves, move{
				account: shopKey, kind: "withdrawal", availableDelta: -amount,
				refType: "payment-session", refID: sessionID,
				idemKey: fmt.Sprintf("withdrawal:%d", sessionID),
				note:    "Rút tiền về tài khoản Vietcombank", at: w.at, group: group,
			})
			sort.SliceStable(moves, func(i, j int) bool { return moves[i].at.Before(moves[j].at) })
		}

		type balance struct {
			available int64
			held      int64
			seq       int64
			openedAt  time.Time
		}
		state := map[string]*balance{}
		ledger := &pgx.Batch{}

		for _, m := range moves {
			b := state[m.account]
			if b == nil {
				b = &balance{openedAt: m.at}
				state[m.account] = b
				const open = `
					INSERT INTO wallet (account_id, currency, available_balance, held_balance, created_at)
					VALUES (@account_id, @currency, 0, 0, @created_at)
					ON CONFLICT (account_id, currency) DO NOTHING`
				if _, err := tx.Exec(ctx, open, pgx.NamedArgs{
					"account_id": parties[m.account].id,
					"currency":   currency,
					"created_at": m.at,
				}); err != nil {
					return fmt.Errorf("open wallet for %s: %w", m.account, err)
				}
			}
			b.available += m.availableDelta
			b.held += m.heldDelta
			b.seq++
			if b.available < 0 || b.held < 0 {
				return fmt.Errorf("ledger for %s goes negative at seq %d (%s): available %d, held %d",
					m.account, b.seq, m.idemKey, b.available, b.held)
			}
			ledger.Queue(`
				INSERT INTO wallet_transaction (account_id, currency, seq, kind,
				                                available_delta, held_delta,
				                                available_after, held_after,
				                                group_id, ref_type, ref_id,
				                                idempotency_key, note, created_at)
				VALUES (@account_id, @currency, @seq, @kind,
				        @available_delta, @held_delta, @available_after, @held_after,
				        @group_id, @ref_type, @ref_id, @idempotency_key, @note, @created_at)`,
				pgx.NamedArgs{
					"account_id":      parties[m.account].id,
					"currency":        currency,
					"seq":             b.seq,
					"kind":            m.kind,
					"available_delta": m.availableDelta,
					"held_delta":      m.heldDelta,
					"available_after": b.available,
					"held_after":      b.held,
					"group_id":        m.group,
					"ref_type":        dbx.NullText(m.refType),
					"ref_id":          dbx.NullID(m.refID),
					"idempotency_key": m.idemKey,
					"note":            m.note,
					"created_at":      m.at,
				})
			written++
		}
		if err := tx.SendBatch(ctx, ledger).Close(); err != nil {
			return fmt.Errorf("insert wallet ledger: %w", err)
		}

		// The balance columns are the last movement's running total. They are a cache of the
		// ledger, and the two disagreeing is the one bug this whole module is shaped to avoid.
		for key, b := range state {
			const q = `
				UPDATE wallet SET available_balance = @available, held_balance = @held,
				                  created_at = @created_at
				WHERE account_id = @account_id AND currency = @currency`
			if _, err := tx.Exec(ctx, q, pgx.NamedArgs{
				"available":  b.available,
				"held":       b.held,
				"created_at": b.openedAt,
				"account_id": parties[key].id,
				"currency":   currency,
			}); err != nil {
				return fmt.Errorf("update wallet balance for %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// minAvailableFrom is the lowest the account's available balance gets at or after `from`, given
// the movements already planned. Anything larger than this taken out at `from` would push a
// later row below zero, which both the wallet and the ledger have a CHECK against.
func minAvailableFrom(moves []move, account string, from time.Time) int64 {
	var running, lowest int64
	seen := false
	for _, m := range moves {
		if m.account != account {
			continue
		}
		running += m.availableDelta
		if m.at.Before(from) {
			continue
		}
		if !seen || running < lowest {
			lowest, seen = running, true
		}
	}
	if !seen {
		lowest = running
	}
	return max(lowest, 0)
}

// roundDown makes an amount look like one a person typed rather than one a computer derived.
func roundDown(v, step int64) int64 {
	if step <= 0 {
		return v
	}
	return v / step * step
}

func writeBankAccount(ctx context.Context, tx pgx.Tx, accountID int64, at time.Time) (int64, error) {
	const q = `
		INSERT INTO bank_account (account_id, bank_code, account_number, account_holder,
		                          is_default, created_at)
		VALUES (@account_id, 'vcb', @account_number, @account_holder, true, @created_at)
		RETURNING id`
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"account_id":     accountID,
		"account_number": "0071000512345",
		"account_holder": "NGUYEN VAN BOB",
		"created_at":     at,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert bank account: %w", err)
	}
	return id, nil
}

// writeWithdrawalSession is a cash-out: a session of kind 'withdrawal' whose destination lives
// in "data", which is where the admin queue reads it from. There is no withdrawal table.
func writeWithdrawalSession(ctx context.Context, tx pgx.Tx, accountID, bankID, amount int64, status string, at time.Time) (int64, error) {
	const q = `
		INSERT INTO payment_session (kind, status, from_id, to_id, note, currency,
		                             total_amount, data, created_at, paid_at, expired_at)
		VALUES ('withdrawal', @status, @from_id, NULL, @note, @currency,
		        @total_amount, @data, @created_at, @paid_at, @expired_at)
		RETURNING id`
	var paidAt any
	if status == "success" {
		paidAt = at.Add(26 * time.Hour)
	}
	var id int64
	err := tx.QueryRow(ctx, q, pgx.NamedArgs{
		"status":       status,
		"from_id":      accountID,
		"note":         "Yêu cầu rút tiền",
		"currency":     currency,
		"total_amount": amount,
		"data":         map[string]any{"bank_account_id": bankID},
		"created_at":   at,
		"paid_at":      paidAt,
		"expired_at":   at.Add(30 * 24 * time.Hour),
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert withdrawal session: %w", err)
	}
	return id, nil
}
