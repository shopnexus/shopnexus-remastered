//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/infra/postgres"
	financepg "shopnexus/internal/module/finance/adapter/postgres"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FINANCE_DB_DSN")
	if dsn == "" {
		t.Skip("FINANCE_DB_DSN not set")
	}
	return dsn
}

func newRepo(t *testing.T) (*financepg.Repo, *pgxpool.Pool) {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), testDSN(t), "finance")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return financepg.New(pool), pool
}

// account keeps one test's money out of another's: the ledger is append-only and shared
// across runs, so a fixed id would make the balances depend on history.
func account(t *testing.T) int64 {
	t.Helper()
	return time.Now().UnixNano() % 1_000_000_000
}

// The ledger's contract in one test: a movement writes the balance and its own row, the
// sequence starts at 1 and counts up, and the balances after match what the row claims.
func TestRepo_MoveWritesBalanceAndLedger(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	moves, err := repo.Move(ctx, []port.Leg{{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Credit(domain.WalletKindTopup, 500_000, domain.Ref{}, "", "top-up"),
	}})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(moves) != 1 || moves[0].Seq != 1 || moves[0].AvailableAfter != 500_000 {
		t.Fatalf("movement = %+v, want seq 1 and the new balance", moves[0])
	}
	w, err := repo.FindWallet(ctx, holder, "VND")
	if err != nil {
		t.Fatalf("FindWallet: %v", err)
	}
	if w.AvailableBalance != 500_000 || w.HeldBalance != 0 {
		t.Fatalf("wallet = %+v", w)
	}

	// A second movement continues the sequence.
	moves, err = repo.Move(ctx, []port.Leg{{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Hold(200_000, domain.OrderRef(1), "", "hold"),
	}})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if moves[0].Seq != 2 || moves[0].HeldAfter != 200_000 || moves[0].AvailableAfter != 300_000 {
		t.Fatalf("movement = %+v, want seq 2 and the split balance", moves[0])
	}
	page, total, err := repo.ListMovements(ctx, port.MovementFilter{
		AccountID: holder, Currency: "VND", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListMovements: %v", err)
	}
	if total != 2 || len(page) != 2 || page[0].Seq != 2 {
		t.Fatalf("ledger = %+v total = %d, want newest first", page, total)
	}
}

// The CHECK constraints and the domain agree: nothing drives a balance negative.
func TestRepo_MoveRefusesOverdraft(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	_, err := repo.Move(ctx, []port.Leg{{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Debit(domain.WalletKindWithdrawal, 1, domain.Ref{}, "", "overdraft"),
	}})
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Fatalf("Move = %v, want ErrInsufficientBalance", err)
	}
	// And nothing was written: a refused movement leaves no ledger row behind, which is what
	// makes the refusal safe to retry. Read the ledger rather than only the wallet — the
	// balance being absent says nothing about a row that names it.
	movements, _, err := repo.ListMovements(ctx, port.MovementFilter{
		AccountID: holder, Currency: "VND", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListMovements: %v", err)
	}
	if len(movements) != 0 {
		t.Fatalf("ledger = %+v, want a refused movement to leave nothing", movements)
	}
	if _, err := repo.FindWallet(ctx, holder, "VND"); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("FindWallet = %v, want no wallet opened by a refused movement", err)
	}
}

// A retried movement loses to the unique index rather than posting the money twice —
// which is what makes an escrow move safe to call again after a lost response.
func TestRepo_MoveIsIdempotent(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)
	key := "test:idempotent:" + time.Now().Format(time.RFC3339Nano)

	leg := port.Leg{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Credit(domain.WalletKindTopup, 1000, domain.Ref{}, key, "top-up"),
	}
	if _, err := repo.Move(ctx, []port.Leg{leg}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := repo.Move(ctx, []port.Leg{leg}); !errors.Is(err, domain.ErrMovementAlreadyPosted) {
		t.Fatalf("second Move = %v, want ErrMovementAlreadyPosted", err)
	}
	w, _ := repo.FindWallet(ctx, holder, "VND")
	if w.AvailableBalance != 1000 {
		t.Fatalf("available = %d, want the money posted once", w.AvailableBalance)
	}
}

// Both legs of an escrow hold land together. If the buyer's debit fails the seller's
// hold must not exist, or the platform has invented money.
func TestRepo_MoveIsAllOrNothing(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	buyer, seller := account(t), account(t)+1

	_, err := repo.Move(ctx, []port.Leg{
		{
			AccountID: buyer, Currency: "VND",
			// Nothing to debit: this leg fails.
			Transfer: domain.Debit(domain.WalletKindEscrowHold, 100, domain.OrderRef(2), "", "debit"),
		},
		{
			AccountID: seller, Currency: "VND",
			Transfer: domain.Transfer{
				Kind: domain.WalletKindEscrowHold, HeldDelta: 100, Note: "hold",
			},
		},
	})
	if !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Fatalf("Move = %v, want the first leg to refuse", err)
	}
	if _, err := repo.FindWallet(ctx, seller, "VND"); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatal("the seller's hold survived a rolled-back movement")
	}
}

// Concurrent movements on one wallet each get their own sequence. Without the row lock
// two would read the same MAX(seq) and the second would collide on the unique index
// after the first had already changed the balance.
func TestRepo_MoveSerialisesTheSequence(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)
	if _, err := repo.Move(ctx, []port.Leg{{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Credit(domain.WalletKindTopup, 1_000, domain.Ref{}, "", "seed"),
	}}); err != nil {
		t.Fatalf("Move: %v", err)
	}

	const movements = 8
	var wg sync.WaitGroup
	errs := make(chan error, movements)
	for i := 0; i < movements; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Move(ctx, []port.Leg{{
				AccountID: holder, Currency: "VND",
				Transfer: domain.Credit(domain.WalletKindTopup, 10, domain.Ref{}, "", "concurrent"),
			}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Move: %v", err)
		}
	}
	w, _ := repo.FindWallet(ctx, holder, "VND")
	if w.AvailableBalance != 1_000+movements*10 {
		t.Fatalf("available = %d, want every movement applied once", w.AvailableBalance)
	}
	page, total, err := repo.ListMovements(ctx, port.MovementFilter{
		AccountID: holder, Currency: "VND", Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListMovements: %v", err)
	}
	if total != movements+1 {
		t.Fatalf("ledger rows = %d, want %d", total, movements+1)
	}
	// The sequence is a total order with no gaps and no repeats.
	seen := map[int64]bool{}
	for _, m := range page {
		if seen[m.Seq] {
			t.Fatalf("sequence %d was handed out twice", m.Seq)
		}
		seen[m.Seq] = true
	}
	for i := int64(1); i <= int64(movements)+1; i++ {
		if !seen[i] {
			t.Errorf("sequence %d is missing", i)
		}
	}
}

// A session and its legs: the transition is guarded by the status, so a redelivered
// settlement finds nothing to update.
func TestRepo_SessionAndLegLifecycle(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	sessionID, err := repo.NextSessionID(ctx)
	if err != nil {
		t.Fatalf("NextSessionID: %v", err)
	}
	session, err := domain.NewSession(sessionID, domain.KindBuyerCheckout, holder, holder+1,
		"test", "VND", 5_000, nil, time.Hour)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := repo.InsertSession(ctx, &session); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	legID, err := repo.NextTransactionID(ctx)
	if err != nil {
		t.Fatalf("NextTransactionID: %v", err)
	}
	leg, err := domain.NewCharge(legID, session.ID, "mock-rail", "VND", 5_000, nil)
	if err != nil {
		t.Fatalf("NewCharge: %v", err)
	}
	if err := repo.InsertTransaction(ctx, &leg); err != nil {
		t.Fatalf("InsertTransaction: %v", err)
	}
	// The provider reference is unique, so it carries the leg's own id: a fixed string
	// would collide with the previous run against the same database.
	if err := leg.Settle(domain.StatusSuccess, fmt.Sprintf("provider-ref-%d", leg.ID), ""); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if err := repo.SaveTransaction(ctx, leg); err != nil {
		t.Fatalf("SaveTransaction: %v", err)
	}
	// The second write finds a settled row: `WHERE status = 'pending'` is the guard that
	// stops a redelivered webhook booking a second settlement.
	if err := repo.SaveTransaction(ctx, leg); !errors.Is(err, domain.ErrTransactionSettled) {
		t.Fatalf("second SaveTransaction = %v, want ErrTransactionSettled", err)
	}

	found, err := repo.FindTransactionByProviderRef(ctx, *leg.ProviderRef)
	if err != nil || found.ID != leg.ID {
		t.Fatalf("FindTransactionByProviderRef = %+v, %v", found, err)
	}
	legs, err := repo.ListTransactions(ctx, session.ID)
	if err != nil || len(legs) != 1 {
		t.Fatalf("ListTransactions = %+v, %v", legs, err)
	}

	session.Status = domain.StatusSuccess
	session.PaidAt = new(time.Now())
	if err := repo.SaveSession(ctx, session, []string{domain.StatusPending, domain.StatusProcessing}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	stored, err := repo.FindSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if stored.Status != domain.StatusSuccess || stored.PaidAt == nil {
		t.Fatalf("session = %+v, want it paid", stored)
	}
	// A party sees it; the filter is what scopes a list to them.
	page, total, err := repo.ListSessions(ctx, port.SessionFilter{AccountID: holder, Limit: 10})
	if err != nil || total == 0 || len(page) == 0 {
		t.Fatalf("ListSessions = %+v total = %d err = %v", page, total, err)
	}
}

// Bank accounts: one default per account, enforced by the partial unique index, and a
// soft delete that refuses while a withdrawal names the row.
func TestRepo_BankAccounts(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	first, err := domain.NewBankAccount(holder, "vcb", "1234567890", "NGUYEN VAN A", true)
	if err != nil {
		t.Fatalf("NewBankAccount: %v", err)
	}
	if err := repo.InsertBankAccount(ctx, &first); err != nil {
		t.Fatalf("InsertBankAccount: %v", err)
	}
	second, err := domain.NewBankAccount(holder, "tcb", "9876543210", "NGUYEN VAN A", true)
	if err != nil {
		t.Fatalf("NewBankAccount: %v", err)
	}
	// The second claiming the default clears the first's in the same transaction — the
	// index would refuse two.
	if err := repo.InsertBankAccount(ctx, &second); err != nil {
		t.Fatalf("InsertBankAccount: %v", err)
	}
	payees, err := repo.ListBankAccounts(ctx, holder)
	if err != nil {
		t.Fatalf("ListBankAccounts: %v", err)
	}
	defaults := 0
	for _, p := range payees {
		if p.IsDefault {
			defaults++
		}
	}
	if len(payees) != 2 || defaults != 1 {
		t.Fatalf("payees = %+v, want two with exactly one default", payees)
	}

	if err := repo.SoftDeleteBankAccount(ctx, first.ID, holder); err != nil {
		t.Fatalf("SoftDeleteBankAccount: %v", err)
	}
	if _, err := repo.FindBankAccount(ctx, first.ID, holder); !errors.Is(err, domain.ErrBankAccountNotFound) {
		t.Fatalf("FindBankAccount after delete = %v", err)
	}
}

// Tax registration: filing again replaces the row and resets the verdict, and only a
// pending one is decidable.
func TestRepo_TaxInfo(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	filed, err := domain.NewTaxInfo(holder, "0123456789", domain.TaxCodeIndividual, "NGUYEN VAN A")
	if err != nil {
		t.Fatalf("NewTaxInfo: %v", err)
	}
	if err := repo.PutTaxInfo(ctx, filed); err != nil {
		t.Fatalf("PutTaxInfo: %v", err)
	}
	stored, err := repo.FindTaxInfo(ctx, holder)
	if err != nil {
		t.Fatalf("FindTaxInfo: %v", err)
	}
	if stored.VerificationStatus != domain.VerificationPending {
		t.Fatalf("status = %q, want it to start pending", stored.VerificationStatus)
	}

	if err := stored.Verify(true, "test"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := repo.SaveTaxInfo(ctx, stored); err != nil {
		t.Fatalf("SaveTaxInfo: %v", err)
	}
	// Deciding again is refused: that would rewrite history rather than add to it.
	if err := repo.SaveTaxInfo(ctx, stored); !errors.Is(err, domain.ErrTaxInfoSettled) {
		t.Fatalf("second SaveTaxInfo = %v, want ErrTaxInfoSettled", err)
	}

	// Filing again resets the verdict, because the details being verified changed.
	refiled, err := domain.NewTaxInfo(holder, "0123456789-001", domain.TaxCodeBusiness, "CONG TY A")
	if err != nil {
		t.Fatalf("NewTaxInfo: %v", err)
	}
	if err := repo.PutTaxInfo(ctx, refiled); err != nil {
		t.Fatalf("PutTaxInfo (refile): %v", err)
	}
	after, err := repo.FindTaxInfo(ctx, holder)
	if err != nil {
		t.Fatalf("FindTaxInfo: %v", err)
	}
	if after.VerificationStatus != domain.VerificationPending || after.VerifiedAt != nil {
		t.Fatalf("tax info = %+v, want the verdict reset", after)
	}
}

// The status guard on a session write, which is the only thing standing between two
// concurrent resolutions of one withdrawal: an approval and a rejection both landing would
// return the money to the wallet and record the payout as sent.
func TestSaveSession_GuardedByTheStatusItMovesFrom(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()

	sessionID, err := repo.NextSessionID(ctx)
	if err != nil {
		t.Fatalf("NextSessionID: %v", err)
	}
	session, err := domain.NewSession(sessionID, domain.KindWithdrawal, account(t), 0,
		"withdrawal", "VND", 200_000, nil, time.Hour)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := repo.InsertSession(ctx, &session); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	live := []string{domain.StatusPending, domain.StatusProcessing}
	// The admin who approves reads `pending` and wins.
	approved := session
	if err := approved.MarkPaid(time.Now()); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if err := repo.SaveSession(ctx, approved, live); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	// The one who rejects read the same `pending` and loses, rather than overwriting a
	// payout that has already been recorded as sent.
	rejected := session
	if err := rejected.MarkFailed(); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := repo.SaveSession(ctx, rejected, live); !errors.Is(err, domain.ErrSessionSettled) {
		t.Fatalf("second SaveSession = %v, want ErrSessionSettled", err)
	}
	stored, err := repo.FindSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if stored.Status != domain.StatusSuccess {
		t.Fatalf("status = %q, want the first decision to stand", stored.Status)
	}
}

// A withdrawal's request and its debit are one write. Apart, a crash between them strands a
// pending cash-out with no debit behind it — and an admin approving that one sends real money
// for a balance that was never reduced.
func TestInsertWithdrawal_IsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := context.Background()
	holder := account(t)

	// Nothing in the wallet, so the debit cannot succeed.
	sessionID, err := repo.NextSessionID(ctx)
	if err != nil {
		t.Fatalf("NextSessionID: %v", err)
	}
	session, err := domain.NewSession(sessionID, domain.KindWithdrawal, holder, 0,
		"withdrawal", "VND", 200_000, nil, time.Hour)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	debit := port.Leg{
		AccountID: holder, Currency: "VND",
		Transfer: domain.Debit(domain.WalletKindWithdrawal, 200_000, domain.SessionRef(session.ID),
			fmt.Sprintf("withdrawal:%d", session.ID), "withdrawal requested"),
	}
	if err := repo.InsertWithdrawal(ctx, &session, debit); !errors.Is(err, domain.ErrInsufficientBalance) {
		t.Fatalf("InsertWithdrawal = %v, want ErrInsufficientBalance", err)
	}
	// And the request is not there either: an unfunded cash-out an admin could approve is
	// exactly what the one transaction exists to prevent.
	if _, err := repo.FindSessionByID(ctx, session.ID); !errors.Is(err, domain.ErrSessionNotFound) {
		t.Fatalf("FindSessionByID = %v, want the session rolled back with the debit", err)
	}
}
