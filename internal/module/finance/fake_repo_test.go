package finance_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/finance/domain"
	"shopnexus/internal/module/finance/port"
)

// fakeRepo is an in-memory port.Repository. It enforces what the schema does — the
// non-negative balances, the per-wallet sequence, the idempotency key, the transitions
// guarded by a status — because those are the rules the service's behaviour rests on and
// a fake that skipped them would let a money bug pass.
type fakeRepo struct {
	nextID   int64
	sessions map[int64]domain.Session
	legs     map[int64]domain.Transaction
	wallets  map[walletKey]domain.Wallet
	ledger   []domain.Movement
	// posted is the idempotency index, which is what makes a retried movement lose
	// rather than double-post.
	posted   map[string]bool
	payees   map[int64]domain.BankAccount
	taxInfos map[int64]domain.TaxInfo
	options  []common.Option
}

type walletKey struct {
	accountID int64
	currency  string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		sessions: map[int64]domain.Session{},
		legs:     map[int64]domain.Transaction{},
		wallets:  map[walletKey]domain.Wallet{},
		posted:   map[string]bool{},
		payees:   map[int64]domain.BankAccount{},
		taxInfos: map[int64]domain.TaxInfo{},
		options:  []common.Option{{ID: "mock-rail", Type: common.OptionTypePayment, IsEnabled: true}},
	}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

var _ port.Repository = (*fakeRepo)(nil)

// ListEnabled makes the fake its own option registry too, so a service test needs no
// second fake for one method.
func (f *fakeRepo) ListEnabled(_ context.Context, optionType string) ([]common.Option, error) {
	var out []common.Option
	for _, o := range f.options {
		if o.Type == optionType && o.IsEnabled {
			out = append(out, o)
		}
	}
	return out, nil
}

// --- sessions and legs ---

func (f *fakeRepo) NextSessionID(context.Context) (int64, error)     { return f.id(), nil }
func (f *fakeRepo) NextTransactionID(context.Context) (int64, error) { return f.id(), nil }

func (f *fakeRepo) InsertSession(_ context.Context, s *domain.Session) error {
	s.CreatedAt = time.Now()
	f.sessions[s.ID] = *s
	return nil
}

func (f *fakeRepo) FindSessionByID(_ context.Context, sessionID int64) (domain.Session, error) {
	s, ok := f.sessions[sessionID]
	if !ok {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeRepo) SaveSession(_ context.Context, s domain.Session) error {
	if _, ok := f.sessions[s.ID]; !ok {
		return domain.ErrSessionNotFound
	}
	f.sessions[s.ID] = s
	return nil
}

func (f *fakeRepo) ListSessions(_ context.Context, filter port.SessionFilter) ([]domain.Session, int64, error) {
	var matched []domain.Session
	for _, s := range f.sessions {
		// Zero is the admin view: every session, whoever is party to it.
		if filter.AccountID != 0 && !s.Involves(filter.AccountID) {
			continue
		}
		if filter.Kind != "" && s.Kind != filter.Kind {
			continue
		}
		if filter.Status != "" && s.Status != filter.Status {
			continue
		}
		matched = append(matched, s)
	}
	slices.SortFunc(matched, func(a, b domain.Session) int { return int(b.ID - a.ID) })
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

func (f *fakeRepo) InsertTransaction(_ context.Context, t *domain.Transaction) error {
	t.CreatedAt = time.Now()
	f.legs[t.ID] = *t
	return nil
}

// SaveTransaction only settles a pending leg, as `WHERE status = 'pending'` does: a
// webhook delivered twice finds nothing to update.
func (f *fakeRepo) SaveTransaction(_ context.Context, t domain.Transaction) error {
	stored, ok := f.legs[t.ID]
	if !ok {
		return domain.ErrTransactionNotFound
	}
	if stored.Status != domain.StatusPending {
		return domain.ErrTransactionSettled
	}
	f.legs[t.ID] = t
	return nil
}

func (f *fakeRepo) ListTransactions(_ context.Context, sessionID int64) ([]domain.Transaction, error) {
	var out []domain.Transaction
	for _, t := range f.legs {
		if t.SessionID == sessionID {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Transaction) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeRepo) FindTransactionByID(_ context.Context, legID int64) (domain.Transaction, error) {
	t, ok := f.legs[legID]
	if !ok {
		return domain.Transaction{}, domain.ErrTransactionNotFound
	}
	return t, nil
}

func (f *fakeRepo) FindTransactionByProviderRef(_ context.Context, ref string) (domain.Transaction, error) {
	for _, t := range f.legs {
		if t.ProviderRef != nil && *t.ProviderRef == ref {
			return t, nil
		}
	}
	return domain.Transaction{}, domain.ErrTransactionNotFound
}

// --- wallets ---

func (f *fakeRepo) FindWallet(_ context.Context, accountID int64, currency string) (domain.Wallet, error) {
	w, ok := f.wallets[walletKey{accountID, currency}]
	if !ok {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	return w, nil
}

func (f *fakeRepo) ListWallets(_ context.Context, accountID int64) ([]domain.Wallet, error) {
	var out []domain.Wallet
	for key, w := range f.wallets {
		if key.accountID == accountID {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b domain.Wallet) int { return strings.Compare(a.Currency, b.Currency) })
	return out, nil
}

func (f *fakeRepo) ListMovements(_ context.Context, filter port.MovementFilter) ([]domain.Movement, int64, error) {
	var matched []domain.Movement
	for _, m := range f.ledger {
		if m.AccountID == filter.AccountID && m.Currency == filter.Currency {
			matched = append(matched, m)
		}
	}
	// Newest first, which is the reverse of the sequence.
	slices.SortFunc(matched, func(a, b domain.Movement) int { return int(b.Seq - a.Seq) })
	total := int64(len(matched))
	if filter.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, len(matched))], total, nil
}

// Move applies every leg or none, as the transaction does. The idempotency key is
// checked before anything moves, so a retried call leaves the balances alone.
func (f *fakeRepo) Move(_ context.Context, legs []port.Leg) ([]domain.Movement, error) {
	if len(legs) == 0 {
		return nil, nil
	}
	// Work on copies so a refusal halfway leaves the wallets untouched.
	staged := map[walletKey]domain.Wallet{}
	var out []domain.Movement
	groupID := f.id()
	for _, leg := range legs {
		key := walletKey{leg.AccountID, leg.Currency}
		w, ok := staged[key]
		if !ok {
			stored, exists := f.wallets[key]
			if !exists {
				// A wallet is not registered: it exists the first time money arrives.
				stored = domain.Wallet{AccountID: leg.AccountID, Currency: leg.Currency}
			}
			w = stored
		}
		if leg.Transfer.IdempotencyKey != nil && f.posted[*leg.Transfer.IdempotencyKey] {
			return nil, domain.ErrMovementAlreadyPosted
		}
		transfer := leg.Transfer
		if transfer.GroupID == nil {
			transfer.GroupID = &groupID
		}
		m, err := w.Apply(transfer, f.nextSeq(leg.AccountID, leg.Currency, out))
		if err != nil {
			return nil, err
		}
		staged[key] = w
		out = append(out, m)
	}
	for key, w := range staged {
		f.wallets[key] = w
	}
	for _, m := range out {
		m.ID = f.id()
		m.CreatedAt = time.Now()
		f.ledger = append(f.ledger, m)
		if m.IdempotencyKey != nil {
			f.posted[*m.IdempotencyKey] = true
		}
	}
	return out, nil
}

// nextSeq counts what is stored plus what this call has already staged, which is what
// the row lock buys the adapter.
func (f *fakeRepo) nextSeq(accountID int64, currency string, staged []domain.Movement) int64 {
	var seq int64
	for _, m := range f.ledger {
		if m.AccountID == accountID && m.Currency == currency && m.Seq > seq {
			seq = m.Seq
		}
	}
	for _, m := range staged {
		if m.AccountID == accountID && m.Currency == currency && m.Seq > seq {
			seq = m.Seq
		}
	}
	return seq + 1
}

// --- bank accounts ---

func (f *fakeRepo) InsertBankAccount(_ context.Context, b *domain.BankAccount) error {
	if b.IsDefault {
		f.clearDefaultPayee(b.AccountID)
	}
	b.ID = f.id()
	b.CreatedAt = time.Now()
	f.payees[b.ID] = *b
	return nil
}

func (f *fakeRepo) FindBankAccount(_ context.Context, payeeID, accountID int64) (domain.BankAccount, error) {
	b, ok := f.payees[payeeID]
	if !ok || b.AccountID != accountID || !b.IsLive() {
		return domain.BankAccount{}, domain.ErrBankAccountNotFound
	}
	return b, nil
}

func (f *fakeRepo) ListBankAccounts(_ context.Context, accountID int64) ([]domain.BankAccount, error) {
	var out []domain.BankAccount
	for _, b := range f.payees {
		if b.AccountID == accountID && b.IsLive() {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b domain.BankAccount) int {
		if a.IsDefault != b.IsDefault {
			if a.IsDefault {
				return -1
			}
			return 1
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

func (f *fakeRepo) SaveBankAccount(_ context.Context, b domain.BankAccount) error {
	stored, ok := f.payees[b.ID]
	if !ok || stored.AccountID != b.AccountID || !stored.IsLive() {
		return domain.ErrBankAccountNotFound
	}
	if b.IsDefault {
		f.clearDefaultPayee(b.AccountID)
	}
	f.payees[b.ID] = b
	return nil
}

// SoftDeleteBankAccount refuses while a withdrawal names it, as the NOT EXISTS does.
func (f *fakeRepo) SoftDeleteBankAccount(_ context.Context, payeeID, accountID int64) error {
	b, ok := f.payees[payeeID]
	if !ok || b.AccountID != accountID || !b.IsLive() {
		return domain.ErrBankAccountInUse
	}
	for _, s := range f.sessions {
		if s.Kind != domain.KindWithdrawal || s.Settled() {
			continue
		}
		if strings.Contains(string(s.Data), fmt.Sprintf(`"bank_account_id":%d`, payeeID)) {
			return domain.ErrBankAccountInUse
		}
	}
	b.DeletedAt = new(time.Now())
	f.payees[payeeID] = b
	return nil
}

func (f *fakeRepo) clearDefaultPayee(accountID int64) {
	for key, b := range f.payees {
		if b.AccountID == accountID && b.IsDefault {
			b.IsDefault = false
			f.payees[key] = b
		}
	}
}

// --- tax info ---

func (f *fakeRepo) PutTaxInfo(_ context.Context, t domain.TaxInfo) error {
	// tax_info_tax_code_verified_uq: only one account may hold a verified code.
	for _, stored := range f.taxInfos {
		if stored.AccountID != t.AccountID && stored.TaxCode == t.TaxCode &&
			stored.VerificationStatus == domain.VerificationVerified {
			return domain.ErrTaxCodeTaken
		}
	}
	t.CreatedAt, t.UpdatedAt = time.Now(), time.Now()
	f.taxInfos[t.AccountID] = t
	return nil
}

func (f *fakeRepo) FindTaxInfo(_ context.Context, accountID int64) (domain.TaxInfo, error) {
	t, ok := f.taxInfos[accountID]
	if !ok {
		return domain.TaxInfo{}, domain.ErrTaxInfoNotFound
	}
	return t, nil
}

func (f *fakeRepo) SaveTaxInfo(_ context.Context, t domain.TaxInfo) error {
	stored, ok := f.taxInfos[t.AccountID]
	if !ok {
		return domain.ErrTaxInfoNotFound
	}
	if stored.VerificationStatus != domain.VerificationPending {
		return domain.ErrTaxInfoSettled
	}
	f.taxInfos[t.AccountID] = t
	return nil
}
