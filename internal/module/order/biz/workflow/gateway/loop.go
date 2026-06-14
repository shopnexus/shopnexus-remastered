package gateway

import (
	"encoding/json"
	"fmt"
	"time"

	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	restate "github.com/restatedev/sdk-go"
)

// LoopParams configures RunPaymentLoop per workflow (amounts, currency, error mapping).
type LoopParams struct {
	SessionID       uuid.UUID
	SessionDeadline time.Time
	NotePrefix      string
	Description     string
	PaymentOption   string
	Amount          int64
	Currency        string
	ErrCancelled    error
	ErrExpired      error
}

// RejectPendingURLs rejects every payment-URL promise a caller could still be
// awaiting — the initial submit waits on url_1, a RequestNewURL caller waits on
// url_<attempt+1> — so neither hangs when Run fails terminally mid-attempt.
// payment_attempt is 0 if the loop never started (covers url_1 only).
func (g *Gateway) RejectPendingURLs(ctx restate.WorkflowContext, cause error) error {
	st, err := restate.Get[*gateState](ctx, gateStateKey)
	if err != nil {
		return fmt.Errorf("read gate state: %w", err)
	}
	attempt := 0
	if st != nil {
		attempt = st.Attempt
	}
	for i := 1; i <= attempt+1; i++ {
		_ = restate.Promise[string](ctx, paymentURLKey(i)).Reject(cause)
	}
	return nil
}

// SettleWalletOnly finalizes a session that has no gateway leg (wallet covered
// the full amount, or zero total): marks the session paid and resolves
// payment_url_1 with "" so a synchronous GetPaymentURL caller returns at once
// instead of blocking on a redirect URL that will never be minted.
func (g *Gateway) SettleWalletOnly(ctx restate.WorkflowContext, sessionID uuid.UUID) error {
	if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		_, e := g.core.Storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
			ID:       sessionID,
			DatePaid: time.Now(),
		})
		return e
	}); err != nil {
		return fmt.Errorf("mark session success (wallet-only): %w", err)
	}
	_ = restate.Promise[string](ctx, paymentURLKey(1)).Resolve("")
	return nil
}

// RunPaymentLoop drives the multi-attempt gateway payment leg. On attempt expiry it lazily
// waits for the caller to call RequestNewURL before charging again.
func (g *Gateway) RunPaymentLoop(ctx restate.WorkflowContext, p LoopParams) error {
	cancel := restate.Promise[struct{}](ctx, cancelKey)

	for attempt := 1; ; attempt++ {
		g.setGate(ctx, gateState{Attempt: attempt, Status: gateCharging})
		txID := restate.UUID(ctx)

		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := g.core.Storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:            txID,
				SessionID:     p.SessionID,
				Status:        orderdb.OrderStatusPending,
				Note:          fmt.Sprintf("%s (attempt %d)", p.NotePrefix, attempt),
				PaymentOption: null.StringFrom(p.PaymentOption),
				Data:          json.RawMessage("{}"),
				Amount:        p.Amount,
				Currency:      p.Currency,
				DateExpired:   null.TimeFrom(time.Now().Add(paymentExpiry)),
			})
			return e
		}); err != nil {
			return fmt.Errorf("create gateway tx: %w", err)
		}

		url, err := g.charge(ctx, txID, p.Amount, p.PaymentOption, fmt.Sprintf("%s (attempt %d)", p.Description, attempt))
		if err != nil {
			return err
		}
		if err = restate.Promise[string](ctx, paymentURLKey(attempt)).Resolve(url); err != nil {
			return fmt.Errorf("resolve payment url: %w", err)
		}
		g.setGate(ctx, gateState{Attempt: attempt, URL: url, Status: gateActive})

		paid := restate.Promise[payment.Notification](ctx, paymentEventKey(txID.String()))
		sessionExp, err := g.sessionExpiry(ctx, p.SessionDeadline, p.ErrExpired)
		if err != nil {
			return err
		}

		done, err := restate.WaitFirst(ctx, paid, cancel, restate.After(ctx, paymentExpiry), sessionExp)
		if err != nil {
			return fmt.Errorf("wait payment event: %w", err)
		}
		switch done {
		case cancel:
			g.setGate(ctx, gateState{Attempt: attempt, Status: gateCancelled})
			return p.ErrCancelled
		case sessionExp:
			g.setGate(ctx, gateState{Attempt: attempt, Status: gateExpired})
			return p.ErrExpired
		case paid:
			ev, evErr := paid.Result()
			if evErr != nil {
				return fmt.Errorf("read payment event: %w", evErr)
			}
			if ev.Status != payment.StatusSuccess {
				return ordermodel.ErrPaymentFailed
			}
			// Pending-guarded UPDATEs → idempotent on replay.
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				now := time.Now()
				if _, e := g.core.Storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{ID: txID, DateSettled: now}); e != nil {
					return fmt.Errorf("mark gateway tx success: %w", e)
				}
				if _, e := g.core.Storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{ID: p.SessionID, DatePaid: now}); e != nil {
					return fmt.Errorf("mark session success: %w", e)
				}
				return nil
			}); err != nil {
				return err
			}
			g.setGate(ctx, gateState{Attempt: attempt, Status: gatePaid})
			return nil
		default: // attempt expired — lazy retry: don't burn gateway quota until the user returns.
			if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				return g.core.Storage.Querier().MarkTransactionsFailed(rctx, orderdb.MarkTransactionsFailedParams{
					ID:    []uuid.UUID{txID},
					Error: null.StringFrom("gateway attempt expired"),
				})
			}); err != nil {
				return fmt.Errorf("mark attempt failed: %w", err)
			}
			g.setGate(ctx, gateState{Attempt: attempt, Status: gateRetry})
			retry := restate.Promise[struct{}](ctx, retryKey(attempt))
			sessionExp2, err := g.sessionExpiry(ctx, p.SessionDeadline, p.ErrExpired)
			if err != nil {
				return err
			}
			done2, err := restate.WaitFirst(ctx, retry, cancel, sessionExp2)
			if err != nil {
				return err
			}
			switch done2 {
			case cancel:
				g.setGate(ctx, gateState{Attempt: attempt, Status: gateCancelled})
				return p.ErrCancelled
			case sessionExp2:
				g.setGate(ctx, gateState{Attempt: attempt, Status: gateExpired})
				return p.ErrExpired
			}
		}
	}
}

// charge calls the payment gateway and persists the redirect URL on the tx. Returns "" for zero amount.
func (g *Gateway) charge(ctx restate.WorkflowContext, txID uuid.UUID, amount int64, option, description string) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	client, err := g.core.GetPaymentClient(option)
	if err != nil {
		return "", fmt.Errorf("get payment client: %w", err)
	}
	url, err := restate.Run(ctx, func(rctx restate.RunContext) (string, error) {
		r, e := client.Charge(rctx, payment.ChargeParams{RefID: txID.String(), Amount: amount, Description: description})
		if e != nil {
			return "", e
		}
		return r.RedirectURL, nil
	})
	if err != nil {
		return "", fmt.Errorf("charge gateway: %w", err)
	}
	if url != "" {
		if err = restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			data, _ := json.Marshal(map[string]string{"gateway_url": url})
			return g.core.Storage.Querier().SetTransactionData(rctx, orderdb.SetTransactionDataParams{ID: txID, Data: data})
		}); err != nil {
			return "", fmt.Errorf("persist gateway url: %w", err)
		}
	}
	return url, nil
}

// sessionExpiry returns a future firing at the deadline, or the expired error if it already passed.
func (g *Gateway) sessionExpiry(ctx restate.WorkflowContext, deadline time.Time, expired error) (restate.Future, error) {
	rem := time.Until(deadline)
	if rem <= 0 {
		return nil, expired
	}
	return restate.After(ctx, rem), nil
}
