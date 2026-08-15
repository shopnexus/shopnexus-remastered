package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
)

// The wipe: everything this command generated, and nothing else.
//
// The line it will not cross is the one between demo data and bootstrap. Bootstrap is what the
// system does not run without — the rows in "option" that name a live payment rail and carrier,
// the category tree a listing cannot exist without, the support desk account every ticket thread
// is a side of, the roles, and the three accounts the report signs in as together with the
// profile, contact, wallet and role rows that make them work. None of that is touched here, at
// any flag, ever. Removing an operator's payment rail because a demo was being reset is not a
// tidy-up, it is an outage.
//
// What it does remove is scoped by *account*: the seeded cast is a fixed list of usernames and
// emails in accounts.go, so a row belongs to the seed if one of those accounts is a party to it.
// A real user's order is never in that set, which is what makes this safe to point at a database
// that has both.
//
// It is idempotent. Running it twice deletes nothing the second time, which is what makes
// "wipe, seed, look at it, wipe, seed again" a workflow rather than a gamble.

type wipeCounts struct {
	accounts, listings, orders, offers, refunds int64
	conversations, messages, reviews, tickets   int64
	walletRows, sessions, resources, files      int64
}

func wipe(ctx context.Context, db *pools, storageRoot string) (wipeCounts, error) {
	var c wipeCounts

	seedIDs, protectedIDs, deletableIDs, err := seedAccountIDs(ctx, db.account)
	if err != nil {
		return c, err
	}
	if len(seedIDs) == 0 {
		log.Printf("wipe: no seeded accounts found; nothing to remove")
		return c, nil
	}
	log.Printf("wipe: %d seeded accounts (%d protected, %d to remove)",
		len(seedIDs), len(protectedIDs), len(deletableIDs))

	// The order ids have to be read before the rows go, because trust's outcome table names
	// them from another schema with no foreign key to follow.
	orderIDs, err := seedOrderIDs(ctx, db.order, seedIDs)
	if err != nil {
		return c, err
	}

	if err := wipeFinance(ctx, db.finance, seedIDs, protectedIDs, &c); err != nil {
		return c, fmt.Errorf("wipe finance: %w", err)
	}
	if err := wipeTrust(ctx, db.trust, seedIDs, orderIDs, &c); err != nil {
		return c, fmt.Errorf("wipe trust: %w", err)
	}
	if err := wipeChat(ctx, db.chat, seedIDs, &c); err != nil {
		return c, fmt.Errorf("wipe chat: %w", err)
	}
	if err := wipeOrder(ctx, db.order, seedIDs, &c); err != nil {
		return c, fmt.Errorf("wipe order: %w", err)
	}
	if err := wipeCatalog(ctx, db.catalog, seedIDs, &c); err != nil {
		return c, fmt.Errorf("wipe catalog: %w", err)
	}
	if err := wipeAccounts(ctx, db.account, deletableIDs, &c); err != nil {
		return c, fmt.Errorf("wipe accounts: %w", err)
	}
	files, err := wipeObjects(storageRoot)
	if err != nil {
		return c, fmt.Errorf("wipe objects: %w", err)
	}
	c.files = files
	return c, nil
}

func seedAccountIDs(ctx context.Context, pool *pgxpool.Pool) (all, protected, deletable []int64, err error) {
	protectedUsers, protectedEmails := protectedIdentifiers()
	deletableUsers, deletableEmails := deletableIdentifiers()

	load := func(users, emails []string) ([]int64, error) {
		const q = `
			SELECT id FROM account
			WHERE username = ANY(@users::text[]) OR email = ANY(@emails::text[])`
		rows, err := pool.Query(ctx, q, pgx.NamedArgs{"users": users, "emails": emails})
		if err != nil {
			return nil, fmt.Errorf("find seeded accounts: %w", err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("scan account id: %w", err)
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}

	if protected, err = load(protectedUsers, protectedEmails); err != nil {
		return nil, nil, nil, err
	}
	if deletable, err = load(deletableUsers, deletableEmails); err != nil {
		return nil, nil, nil, err
	}
	all = append(append([]int64{}, protected...), deletable...)
	return all, protected, deletable, nil
}

func seedOrderIDs(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64) ([]int64, error) {
	const q = `SELECT id FROM "order" WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids)`
	rows, err := pool.Query(ctx, q, pgx.NamedArgs{"ids": seedIDs})
	if err != nil {
		return nil, fmt.Errorf("find seeded orders: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan order id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func wipeFinance(ctx context.Context, pool *pgxpool.Pool, seedIDs, protectedIDs []int64, c *wipeCounts) error {
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs, "protected": protectedIDs}
		// Rail legs before the sessions they hang off, and the wallet ledger before the
		// wallets it has a foreign key onto.
		for _, stmt := range []string{
			`DELETE FROM transaction WHERE session_id IN (
				SELECT id FROM payment_session WHERE from_id = ANY(@ids) OR to_id = ANY(@ids))`,
			`DELETE FROM wallet_transaction WHERE account_id = ANY(@ids)`,
		} {
			if _, err := tx.Exec(ctx, stmt, args); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx,
			`DELETE FROM payment_session WHERE from_id = ANY(@ids) OR to_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.sessions = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `DELETE FROM bank_account WHERE account_id = ANY(@ids)`, args); err != nil {
			return err
		}
		// A protected account keeps its wallet row — it is one of the rows that make the
		// account work — but its demo balance goes back to zero, because the ledger that
		// justified the number has just been removed and a balance with nothing behind it is
		// a lie the earnings screen would repeat.
		if _, err := tx.Exec(ctx,
			`UPDATE wallet SET available_balance = 0, held_balance = 0 WHERE account_id = ANY(@protected)`,
			args); err != nil {
			return err
		}
		tag, err = tx.Exec(ctx,
			`DELETE FROM wallet WHERE account_id = ANY(@ids) AND NOT (account_id = ANY(@protected))`, args)
		if err != nil {
			return err
		}
		c.walletRows = tag.RowsAffected()
		return deleteSeedResources(ctx, tx, c)
	})
}

func wipeTrust(ctx context.Context, pool *pgxpool.Pool, seedIDs, orderIDs []int64, c *wipeCounts) error {
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs, "orders": orderIDs}
		if _, err := tx.Exec(ctx, `DELETE FROM review_vote WHERE account_id = ANY(@ids)`, args); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM review_reply WHERE author_id = ANY(@ids)`, args); err != nil {
			return err
		}
		// Replies and votes on these go with them: both have ON DELETE CASCADE.
		tag, err := tx.Exec(ctx,
			`DELETE FROM review WHERE author_id = ANY(@ids) OR seller_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.reviews = tag.RowsAffected()

		for _, stmt := range []string{
			`DELETE FROM feedback WHERE rater_id = ANY(@ids) OR ratee_id = ANY(@ids)`,
			`DELETE FROM order_outcome WHERE order_id = ANY(@orders)`,
			`DELETE FROM reputation WHERE account_id = ANY(@ids)`,
		} {
			if _, err := tx.Exec(ctx, stmt, args); err != nil {
				return err
			}
		}
		tag, err = tx.Exec(ctx, `DELETE FROM ticket WHERE requester_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.tickets = tag.RowsAffected()
		return deleteSeedResources(ctx, tx, c)
	})
}

func wipeChat(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64, c *wipeCounts) error {
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs}
		// Count the messages before the cascade takes them, because a cascaded delete does
		// not report what it removed.
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM message WHERE conversation_id IN (
				SELECT id FROM conversation
				WHERE account_a_id = ANY(@ids) OR account_b_id = ANY(@ids))`, args,
		).Scan(&c.messages); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			DELETE FROM conversation
			WHERE account_a_id = ANY(@ids) OR account_b_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.conversations = tag.RowsAffected()
		return deleteSeedResources(ctx, tx, c)
	})
}

func wipeOrder(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64, c *wipeCounts) error {
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs}
		const scope = `SELECT id FROM "order" WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids)`

		tag, err := tx.Exec(ctx, `DELETE FROM refund WHERE order_id IN (`+scope+`)`, args)
		if err != nil {
			return err
		}
		c.refunds = tag.RowsAffected()

		// The line records money the buyer paid, so its foreign key onto the order is NO
		// ACTION rather than CASCADE. It has to go first and by hand.
		if _, err := tx.Exec(ctx,
			`DELETE FROM item WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids)`, args); err != nil {
			return err
		}
		tag, err = tx.Exec(ctx, `DELETE FROM "order" WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.orders = tag.RowsAffected()

		for _, stmt := range []string{
			`DELETE FROM draft_order WHERE buyer_id = ANY(@ids)`,
			`DELETE FROM cart_item WHERE account_id = ANY(@ids)`,
			// Shipments are addressed by the order and the refund that held them, both of
			// which are gone; what identifies the rest as ours is the reference the seeder
			// stamped on them.
			`DELETE FROM transport WHERE data->>'provider_ref' LIKE 'SEED-%'`,
		} {
			if _, err := tx.Exec(ctx, stmt, args); err != nil {
				return err
			}
		}
		tag, err = tx.Exec(ctx,
			`DELETE FROM offer WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.offers = tag.RowsAffected()
		return deleteSeedResources(ctx, tx, c)
	})
}

func wipeCatalog(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64, c *wipeCounts) error {
	d, err := loadDataset()
	if err != nil {
		return err
	}
	tags := map[string]bool{}
	for _, l := range d.Listings {
		for _, t := range l.Tags {
			tags[t] = true
		}
	}
	tagIDs := make([]string, 0, len(tags))
	for t := range tags {
		tagIDs = append(tagIDs, t)
	}

	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs, "tags": tagIDs}
		if _, err := tx.Exec(ctx, `DELETE FROM favorite WHERE account_id = ANY(@ids)`, args); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM account_interest WHERE account_id = ANY(@ids)`, args); err != nil {
			return err
		}
		// Variants, stock, tag links, embeddings and favourites all cascade off the listing.
		tag, err := tx.Exec(ctx, `DELETE FROM listing WHERE account_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		c.listings = tag.RowsAffected()

		// A tag this seeder introduced and nothing else uses. Anything a real listing still
		// carries stays: the dictionary is shared, and emptying it because a demo was reset
		// would take a stranger's tag with it.
		if _, err := tx.Exec(ctx, `
			DELETE FROM tag WHERE id = ANY(@tags::text[])
			  AND NOT EXISTS (SELECT 1 FROM listing_tag WHERE tag = tag.id)`, args); err != nil {
			return err
		}
		// Categories are deliberately absent from this function. See the note at the top.
		return deleteSeedResources(ctx, tx, c)
	})
}

// deleteSeedResources removes the rows pointing at objects this command wrote, in whichever
// schema the transaction is aimed at. The key prefix is the marker: every object the seeder
// creates lives under "seed/", and nothing else does.
func deleteSeedResources(ctx context.Context, tx pgx.Tx, c *wipeCounts) error {
	tag, err := tx.Exec(ctx,
		`DELETE FROM resource WHERE provider = 'local' AND object_key LIKE 'seed/%'`)
	if err != nil {
		return fmt.Errorf("delete seeded resources: %w", err)
	}
	c.resources += tag.RowsAffected()
	return nil
}

func wipeAccounts(ctx context.Context, pool *pgxpool.Pool, deletableIDs []int64, c *wipeCounts) error {
	if len(deletableIDs) == 0 {
		return nil
	}
	return dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		// Contacts, devices, notifications, follows and linked identities all cascade off the
		// account row. The three protected accounts are not in this list and keep everything.
		tag, err := tx.Exec(ctx,
			`DELETE FROM account WHERE id = ANY(@ids)`, pgx.NamedArgs{"ids": deletableIDs})
		if err != nil {
			return err
		}
		c.accounts = tag.RowsAffected()
		return deleteSeedResources(ctx, tx, c)
	})
}

// wipeObjects removes the generated photographs from the object store. Best effort by design:
// a seeder run outside the gateway's filesystem cannot see them, and a wipe that refuses to
// clean the database because it could not reach a directory is worse than a few orphaned files
// that the next run overwrites anyway.
func wipeObjects(root string) (int64, error) {
	if root == "" {
		return 0, nil
	}
	dir := filepath.Join(root, "seed")
	var n int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("wipe: could not remove %s: %v (database is clean; remove it by hand)", dir, err)
		return 0, nil
	}
	return n, nil
}
