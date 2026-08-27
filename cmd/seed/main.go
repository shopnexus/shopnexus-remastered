// Command seed fills a migrated database with the demo marketplace: a hand-written Vietnamese
// C2C catalogue of second-hand goods, the accounts that trade in it, and the history that gives
// every screen something to show — orders in each state, negotiations with live price cards,
// reviews with replies and votes, support tickets, and the wallet ledger behind all of it.
//
// It writes SQL directly, one transaction per module pool, rather than going through the
// services. There is no "create this listing as that seller with that rating" command and there
// should not be: a rating is earned, and inventing the history that earned it is this command's
// whole job.
//
// # Seed data and bootstrap
//
// Bootstrap is what the system does not run without, and it belongs in migrations:
//
//   - the rows in "option" that name a live payment rail and a carrier (an operator's, since
//     migrations 003 removed the placeholder ones);
//   - the support desk account every ticket thread is a side of (account migration 004);
//   - the roles;
//   - the category tree, because "listing"."category_id" is NOT NULL and a deployment with an
//     empty tree cannot accept a listing at all.
//
// This command creates, modifies and deletes none of the first three. The category tree is the
// exception and it is a known wart: it is written here, for historical reasons, with ON CONFLICT
// on the name and never an UPDATE, and the wipe never removes a category. It should move into a
// catalog migration; moving it is a change to the migration set and is not this command's to
// make quietly.
//
// Seed data is the demo: accounts, listings, orders, conversations, reviews, tickets and wallet
// movements. Only that is generated, and only that is removed.
//
// # Running it
//
//	seed                          # load the demo data; refuses if it is already there
//	seed -wipe -yes-i-mean-it     # remove it again; never runs as part of a load
//
// The two are separate paths on purpose. A loader that quietly wiped first is one bad afternoon
// away from being pointed at the wrong database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/config"
	"shopnexus/internal/infra/postgres"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

func main() {
	var (
		doWipe  = flag.Bool("wipe", false, "remove the seeded demo data instead of loading it")
		confirm = flag.Bool("yes-i-mean-it", false, "required with -wipe; deleting data is not a default")
		photos  = flag.Bool("photos", true, "generate the product photographs into the object store")
	)
	flag.Parse()

	if err := run(context.Background(), *doWipe, *confirm, *photos); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run(ctx context.Context, doWipe, confirm, photos bool) error {
	cfg, err := config.Load(validation.Default())
	if err != nil {
		return err
	}
	// The chat cards carry opaque wire ids, not database keys: a card holding a bigint is one
	// the storefront cannot parse. Same key as the gateway's, or the ids decode to nothing.
	if err := id.SetCipher([]byte(cfg.IDCipherKey)); err != nil {
		return fmt.Errorf("install id cipher: %w", err)
	}

	db, err := openPools(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.close()

	if doWipe {
		if !confirm {
			return errors.New(
				"refusing to wipe without -yes-i-mean-it: this deletes every seeded account, " +
					"listing, order, conversation, review, ticket and wallet movement")
		}
		start := time.Now()
		c, err := wipe(ctx, db, cfg.StorageRoot)
		if err != nil {
			return err
		}
		log.Printf("wiped in %s: %d accounts, %d listings, %d orders, %d offers, %d refunds, "+
			"%d conversations, %d messages, %d reviews, %d tickets, %d wallets, %d payment sessions, "+
			"%d resource rows, %d audit rows, %d stranded refs, %d objects",
			time.Since(start).Round(time.Millisecond),
			c.accounts, c.listings, c.orders, c.offers, c.refunds,
			c.conversations, c.messages, c.reviews, c.tickets,
			c.walletRows, c.sessions, c.resources, c.auditRows, c.strandedRefs, c.files)
		log.Printf("categories, the option registry, the support desk and the three demo logins were left alone")
		return nil
	}
	if confirm {
		return errors.New("-yes-i-mean-it only means anything with -wipe")
	}

	d, err := loadDataset()
	if err != nil {
		return err
	}
	p, err := buildPlan(d, time.Now())
	if err != nil {
		return err
	}
	log.Printf("planned %d listings, %d variants, %d photos (%d real), %d orders, %d offers, %d conversations, %d tickets",
		len(p.listings), countVariants(p), len(p.images)+len(p.evidence), p.realPhotoCount(),
		len(p.orders), len(p.offers), len(p.threads), len(p.tickets))

	if err := checkNotSeeded(ctx, db.account); err != nil {
		return err
	}

	start := time.Now()
	if photos {
		bytes, real, err := writePhotos(cfg.StorageRoot, p.images)
		if err != nil {
			return fmt.Errorf("write listing photos: %w\n"+
				"  the object store at %q is not writable from here. Run the seeder where the\n"+
				"  gateway's storage volume is mounted (docker compose --profile seed run --rm seed),\n"+
				"  or pass -photos=false to seed without them", err, cfg.StorageRoot)
		}
		evidenceBytes, _, err := writePhotos(cfg.StorageRoot, p.evidence)
		if err != nil {
			return fmt.Errorf("write evidence photos: %w", err)
		}
		log.Printf("photos: %d files, %s — %d real (CC0 / public domain, see cmd/seed/photos/ATTRIBUTION.md), %d drawn",
			len(p.images)+len(p.evidence), humanBytes(bytes+evidenceBytes),
			real, len(p.images)+len(p.evidence)-real)
	} else {
		log.Printf("photos: skipped (-photos=false); galleries will render empty")
	}

	accounts, err := writeAccounts(ctx, db.account)
	if err != nil {
		return fmt.Errorf("seed accounts: %w", err)
	}
	parties := make(map[string]party, len(accounts))
	created := 0
	for _, a := range accounts {
		parties[a.Key] = a
		if a.created {
			created++
		}
	}
	log.Printf("accounts: %d in the cast, %d created, %d already existed", len(accounts), created, len(accounts)-created)

	deskID, err := supportDeskID(ctx, db.account)
	if err != nil {
		return err
	}

	cat, err := writeCatalog(ctx, db.catalog, p, parties)
	if err != nil {
		return fmt.Errorf("seed catalog: %w", err)
	}
	log.Printf("catalog: %d categories ensured, %d listings, %d variants",
		len(p.categories), len(cat.listings), countVariants(p))

	amounts, err := computeAmounts(p, parties)
	if err != nil {
		return err
	}
	// The checkout sessions come before the orders that name them: "item"."payment_session_id"
	// is NOT NULL, and a shared zero would group every seeded line into one giant checkout.
	sessions, err := writePaymentSessions(ctx, db.finance, p, parties, amounts)
	if err != nil {
		return fmt.Errorf("seed payment sessions: %w", err)
	}

	sales, err := writeSales(ctx, db.order, p, parties, cat, sessions)
	if err != nil {
		return fmt.Errorf("seed orders: %w", err)
	}
	log.Printf("orders: %d, across %s", len(sales.orderIDs), stateSummary(p))

	ticketIDs, err := writeTrust(ctx, db.trust, p, parties, cat, sales)
	if err != nil {
		return fmt.Errorf("seed trust: %w", err)
	}

	threadIDs, err := writeChat(ctx, db.chat, p, parties, cat, sales, deskID, ticketIDs)
	if err != nil {
		return fmt.Errorf("seed chat: %w", err)
	}
	if err := attachTicketThreads(ctx, db.trust, ticketIDs, threadIDs); err != nil {
		return fmt.Errorf("attach ticket threads: %w", err)
	}
	log.Printf("chat: %d direct threads, %d ticket threads", len(p.threads), len(threadIDs))

	movements, err := writeLedger(ctx, db.finance, p, parties, amounts, sales, sessions)
	if err != nil {
		return fmt.Errorf("seed wallet ledger: %w", err)
	}
	log.Printf("finance: %d payment sessions, %d wallet movements", len(sessions), movements)

	log.Printf("done in %s. the three accounts the report signs in as:", time.Since(start).Round(time.Millisecond))
	for _, a := range seedAccounts {
		if !a.Protected {
			continue
		}
		note := "(password unchanged — the row was already there)"
		if parties[a.Key].created {
			note = a.Password
		}
		log.Printf("  %-10s %-26s %s", a.Username, a.Email, note)
	}
	return nil
}

// Each module gets its own pool on its own DSN with its own search_path, exactly as cmd/migrate
// and the running gateway do. In dev all six point at one database and are kept apart by schema.
type pools struct {
	account *pgxpool.Pool
	catalog *pgxpool.Pool
	order   *pgxpool.Pool
	chat    *pgxpool.Pool
	trust   *pgxpool.Pool
	finance *pgxpool.Pool
}

func openPools(ctx context.Context, cfg *config.Config) (*pools, error) {
	out := &pools{}
	for _, target := range []struct {
		name string
		dsn  string
		dst  **pgxpool.Pool
	}{
		{"account", cfg.AccountDBDSN, &out.account},
		{"catalog", cfg.CatalogDBDSN, &out.catalog},
		{"order", cfg.OrderDBDSN, &out.order},
		{"chat", cfg.ChatDBDSN, &out.chat},
		{"trust", cfg.TrustDBDSN, &out.trust},
		{"finance", cfg.FinanceDBDSN, &out.finance},
	} {
		pool, err := postgres.NewPool(ctx, target.dsn, target.name)
		if err != nil {
			out.close()
			return nil, fmt.Errorf("open %s pool: %w", target.name, err)
		}
		*target.dst = pool
	}
	return out, nil
}

func (p *pools) close() {
	for _, pool := range []*pgxpool.Pool{p.account, p.catalog, p.order, p.chat, p.trust, p.finance} {
		if pool != nil {
			pool.Close()
		}
	}
}

func countVariants(p *plan) int {
	n := 0
	for _, l := range p.listings {
		n += len(l.variants)
	}
	return n
}

// stateSummary is the line worth reading in the log: which of the screens the report needs are
// actually present, and how many of each.
func stateSummary(p *plan) string {
	counts := map[orderState]int{}
	for _, o := range p.orders {
		counts[o.state]++
	}
	order := []orderState{
		stateAwaitingConfirmation, statePreparing, stateInTransit, stateDelivered,
		stateCompleted, stateDeclined, stateRefundRequested, stateRefundDisputed,
		stateRefundAccepted,
	}
	out := ""
	for _, s := range order {
		if counts[s] == 0 {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%s×%d", s, counts[s])
	}
	return out
}

func humanBytes(n int64) string {
	switch {
	case n > 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n > 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
