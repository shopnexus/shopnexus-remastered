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
//
// The audit trail goes with the records it describes. It carries no foreign key onto them — one
// that cascaded would be a trail deleted by the first change it was written for — so every id has
// to reach deleteAuditTrail by hand, and a module that starts auditing a table named nowhere below
// will leave its rows behind, pointing at records that no longer exist.

type wipeCounts struct {
	accounts, listings, orders, offers, refunds int64
	conversations, messages, reviews, tickets   int64
	walletRows, sessions, resources, files      int64
	auditRows, strandedRefs                     int64
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
	// Same reason, the other direction: order's cart lines, drafts and offers name a listing
	// across the schema boundary, and wipeCatalog runs after wipeOrder.
	listingIDs, err := seedListingIDs(ctx, db.catalog, seedIDs)
	if err != nil {
		return c, err
	}

	// The object keys of every resource row that went, module by module. The store is swept
	// from this list rather than by removing the seeder's directory, because one of those rows
	// can be shared with a listing this wipe is not about — see wipeCatalog.
	var keys []string
	for _, step := range []struct {
		name string
		run  func() ([]string, error)
	}{
		{"finance", func() ([]string, error) { return wipeFinance(ctx, db.finance, seedIDs, protectedIDs, &c) }},
		{"trust", func() ([]string, error) { return wipeTrust(ctx, db.trust, seedIDs, orderIDs, &c) }},
		{"chat", func() ([]string, error) { return wipeChat(ctx, db.chat, seedIDs, &c) }},
		{"order", func() ([]string, error) { return wipeOrder(ctx, db.order, seedIDs, listingIDs, &c) }},
		{"catalog", func() ([]string, error) { return wipeCatalog(ctx, db.catalog, seedIDs, deletableIDs, &c) }},
		{"accounts", func() ([]string, error) { return wipeAccounts(ctx, db.account, deletableIDs, &c) }},
	} {
		got, err := step.run()
		if err != nil {
			return c, fmt.Errorf("wipe %s: %w", step.name, err)
		}
		keys = append(keys, got...)
	}
	files, err := wipeObjects(storageRoot, keys)
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
		ids, err := collectIDs(ctx, pool, q, pgx.NamedArgs{"users": users, "emails": emails})
		if err != nil {
			return nil, fmt.Errorf("find seeded accounts: %w", err)
		}
		return ids, nil
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
	ids, err := collectIDs(ctx, pool, q, pgx.NamedArgs{"ids": seedIDs})
	if err != nil {
		return nil, fmt.Errorf("find seeded orders: %w", err)
	}
	return ids, nil
}

func seedListingIDs(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64) ([]int64, error) {
	const q = `SELECT id FROM listing WHERE account_id = ANY(@ids)`
	ids, err := collectIDs(ctx, pool, q, pgx.NamedArgs{"ids": seedIDs})
	if err != nil {
		return nil, fmt.Errorf("find seeded listings: %w", err)
	}
	return ids, nil
}

// idQuerier is the pool or a transaction: a read taken before the wipe runs on the pool, a
// DELETE ... RETURNING runs inside it.
type idQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// collectIDs runs a statement whose one column is an id. The DELETE form is what tells the audit
// trail which records went, since there is no cascade to follow and RowsAffected names no ids.
func collectIDs(ctx context.Context, q idQuerier, stmt string, args pgx.NamedArgs) ([]int64, error) {
	rows, err := q.Query(ctx, stmt, args)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func wipeFinance(ctx context.Context, pool *pgxpool.Pool, seedIDs, protectedIDs []int64, c *wipeCounts) ([]string, error) {
	var keys []string
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
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
		keys, err = deleteSeedResources(ctx, tx, deleteSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

func wipeTrust(ctx context.Context, pool *pgxpool.Pool, seedIDs, orderIDs []int64, c *wipeCounts) ([]string, error) {
	var keys []string
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
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
		keys, err = deleteSeedResources(ctx, tx, deleteSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

func wipeChat(ctx context.Context, pool *pgxpool.Pool, seedIDs []int64, c *wipeCounts) ([]string, error) {
	var keys []string
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
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
		keys, err = deleteSeedResources(ctx, tx, deleteSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

func wipeOrder(ctx context.Context, pool *pgxpool.Pool, seedIDs, listingIDs []int64, c *wipeCounts) ([]string, error) {
	var keys []string
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs, "listings": listingIDs}
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
		orderIDs, err := collectIDs(ctx, tx,
			`DELETE FROM "order" WHERE buyer_id = ANY(@ids) OR seller_id = ANY(@ids) RETURNING id`, args)
		if err != nil {
			return err
		}
		c.orders = int64(len(orderIDs))
		if err := deleteAuditTrail(ctx, tx, "order", orderIDs, c); err != nil {
			return err
		}

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

		// What is left names a listing this wipe is about to delete but belongs to nobody in the
		// cast — a real shopper's cart line, or a draft on a seeded listing, since drafts are
		// scoped by buyer above and offers by either party. The listing is in another schema, so
		// no foreign key can take these along, and one left pointing at a row that no longer
		// exists is a 404 on every page that renders it. Intent only: a draft or an offer that
		// became an order is that order's provenance, and item's foreign key onto it holds.
		for _, stmt := range []string{
			`DELETE FROM cart_item WHERE listing_id = ANY(@listings)`,
			`DELETE FROM draft_order WHERE listing_id = ANY(@listings)
			   AND NOT EXISTS (SELECT 1 FROM item WHERE item.draft_id = draft_order.id)`,
			`DELETE FROM offer WHERE listing_id = ANY(@listings)
			   AND NOT EXISTS (SELECT 1 FROM item WHERE item.offer_id = offer.id)`,
		} {
			tag, err := tx.Exec(ctx, stmt, args)
			if err != nil {
				return err
			}
			c.strandedRefs += tag.RowsAffected()
		}
		keys, err = deleteSeedResources(ctx, tx, deleteSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

func wipeCatalog(ctx context.Context, pool *pgxpool.Pool, seedIDs, deletableIDs []int64, c *wipeCounts) ([]string, error) {
	d, err := loadDataset()
	if err != nil {
		return nil, err
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

	var keys []string
	err = dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": seedIDs, "gone": deletableIDs, "tags": tagIDs}
		// A favourite, a browse signal and an interest rail are none of them the seeder's --
		// nothing in this command writes one -- so they go with the account that owns them and
		// not with the cast. dev/bulkseed/sql/demo_interests.sql builds the protected buyer's
		// rails out of real listings, and a wipe that took those would empty the personalised
		// feed the demo exists to show. Whatever points at a listing deleted below cascades
		// anyway, so scoping this to the accounts that are going loses nothing.
		for _, stmt := range []string{
			`DELETE FROM favorite WHERE account_id = ANY(@gone)`,
			`DELETE FROM account_interest WHERE account_id = ANY(@gone)`,
			`DELETE FROM listing_signal WHERE account_id = ANY(@gone)`,
		} {
			if _, err := tx.Exec(ctx, stmt, args); err != nil {
				return err
			}
		}
		// Variants, stock, tag links, embeddings and favourites all cascade off the listing.
		listingIDs, err := collectIDs(ctx, tx,
			`DELETE FROM listing WHERE account_id = ANY(@ids) RETURNING id`, args)
		if err != nil {
			return err
		}
		c.listings = int64(len(listingIDs))
		if err := deleteAuditTrail(ctx, tx, "listing", listingIDs, c); err != nil {
			return err
		}

		// A tag this seeder introduced and nothing else uses. Anything a real listing still
		// carries stays: the dictionary is shared, and emptying it because a demo was reset
		// would take a stranger's tag with it.
		if _, err := tx.Exec(ctx, `
			DELETE FROM tag WHERE id = ANY(@tags::text[])
			  AND NOT EXISTS (SELECT 1 FROM listing_tag WHERE tag = tag.id)`, args); err != nil {
			return err
		}
		// Categories are deliberately absent from this function. See the note at the top.
		keys, err = deleteSeedResources(ctx, tx, deleteUnsharedSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

// The two statements deleteSeedResources runs, in whichever schema the transaction is aimed at.
// The key prefix is the marker: every object the seeder creates lives under "seed/", and nothing
// else does.
//
// Catalog takes the second one. A seeded photograph there is not the seeder's alone:
// dev/bulkseed points a generated listing with no photograph of its own at whichever gallery was
// already in the catalogue, so tens of thousands of listings this wipe is not about share these
// rows. Deleting one leaves an attachment id resolving to nothing, which is an empty gallery on
// a page that had one -- and no foreign key can say so, because an attachment is an id in an
// array. The anti-join is over the surviving listings, so it runs after they are deleted.
const (
	seedResourceScope = `provider = 'local' AND object_key LIKE 'seed/%'`

	deleteSeedResourcesStmt = `DELETE FROM resource WHERE ` + seedResourceScope +
		` RETURNING object_key`

	deleteUnsharedSeedResourcesStmt = `DELETE FROM resource WHERE ` + seedResourceScope +
		` AND id NOT IN (SELECT unnest(attachments) FROM listing) RETURNING object_key`
)

// deleteSeedResources removes the rows pointing at objects this command wrote and answers the
// keys of the objects that went, which is what the store is swept from: a row that stayed keeps
// its file.
func deleteSeedResources(ctx context.Context, tx pgx.Tx, stmt string, c *wipeCounts) ([]string, error) {
	rows, err := tx.Query(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("delete seeded resources: %w", err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("delete seeded resources: %w", err)
	}
	c.resources += int64(len(keys))
	return keys, nil
}

// deleteAuditTrail removes the trail of records this wipe just deleted, in whichever schema the
// transaction is aimed at. One audit_log holds every audited table of its module side by side, so
// the caller names the one whose ids it is carrying.
func deleteAuditTrail(ctx context.Context, tx pgx.Tx, table string, recordIDs []int64, c *wipeCounts) error {
	if len(recordIDs) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM audit_log WHERE table_name = @table AND record_id = ANY(@ids)`,
		pgx.NamedArgs{"table": table, "ids": recordIDs})
	if err != nil {
		return fmt.Errorf("delete audit trail for %s: %w", table, err)
	}
	c.auditRows += tag.RowsAffected()
	return nil
}

func wipeAccounts(ctx context.Context, pool *pgxpool.Pool, deletableIDs []int64, c *wipeCounts) ([]string, error) {
	if len(deletableIDs) == 0 {
		return nil, nil
	}
	var keys []string
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		args := pgx.NamedArgs{"ids": deletableIDs}
		// The documents cascade off the account, so their ids are read while the rows are still
		// there — what audited them does not cascade with them.
		docIDs, err := collectIDs(ctx, tx,
			`SELECT id FROM identity_document WHERE account_id = ANY(@ids)`, args)
		if err != nil {
			return err
		}
		// Contacts, devices, notifications, follows and linked identities all cascade off the
		// account row. The three protected accounts are not in this list and keep everything.
		accountIDs, err := collectIDs(ctx, tx, `DELETE FROM account WHERE id = ANY(@ids) RETURNING id`, args)
		if err != nil {
			return err
		}
		c.accounts = int64(len(accountIDs))
		if err := deleteAuditTrail(ctx, tx, "account", accountIDs, c); err != nil {
			return err
		}
		if err := deleteAuditTrail(ctx, tx, "identity_document", docIDs, c); err != nil {
			return err
		}
		keys, err = deleteSeedResources(ctx, tx, deleteSeedResourcesStmt, c)
		return err
	})
	return keys, err
}

// wipeObjects removes the objects whose rows have just gone, named one by one. Not the seeder's
// directory: a key a surviving listing still points at keeps its file, or the wipe empties a
// gallery it was never about. Best effort by design — a seeder run outside the gateway's
// filesystem cannot see the store at all, and an object with no row left is unreachable whether
// or not its file went, so a file that is already gone is not an error either.
func wipeObjects(root string, keys []string) (int64, error) {
	if root == "" {
		return 0, nil
	}
	var n int64
	dirs := map[string]bool{}
	for _, key := range keys {
		path := filepath.Join(root, filepath.Clean("/"+key))
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				log.Printf("wipe: could not remove %s: %v (database is clean; remove it by hand)", path, err)
			}
			continue
		}
		n++
		dirs[filepath.Dir(path)] = true
	}
	// Whatever is now empty. os.Remove refuses a directory that is not, which is the test.
	for dir := range dirs {
		_ = os.Remove(dir)
	}
	_ = os.Remove(filepath.Join(root, "seed"))
	return n, nil
}
