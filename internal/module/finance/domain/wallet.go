package domain

// Wallet transaction kinds (kebab-case, mirrors the wallet_txn_kind enum).
const (
	WalletKindTopup         = "topup"
	WalletKindEscrowHold    = "escrow-hold"
	WalletKindEscrowRelease = "escrow-release"
	WalletKindPayout        = "payout"
	WalletKindRefund        = "refund"
	WalletKindWithdrawal    = "withdrawal"
	WalletKindFee           = "fee"
	WalletKindAdjustment    = "adjustment"
)

// Wallet holds one account's balances: available is spendable/withdrawable,
// held is locked in escrow. Both are non-negative (enforced in the schema too).
type Wallet struct {
	AccountID        int64
	Currency         string
	AvailableBalance int64
	HeldBalance      int64
}

// Total is what the account owns, spendable or not.
func (w Wallet) Total() int64 { return w.AvailableBalance + w.HeldBalance }

// CanSpend reports whether amount fits in the available balance.
func (w Wallet) CanSpend(amount int64) bool {
	return amount > 0 && w.AvailableBalance >= amount
}
