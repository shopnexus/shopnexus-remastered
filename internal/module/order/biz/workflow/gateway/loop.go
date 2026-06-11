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

// LoopParams configures RunPaymentLoop for a specific workflow. Differences are
// pure data: amounts, currency, error mapping, log strings.
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

// RunPaymentLoop drives the multi-attempt gateway payment leg. Each iteration
// mints a fresh Pending gateway tx, charges the gateway, resolves the
// attempt's payment-URL promise, then waits on {payment event | cancel |
// attempt expiry | session expiry}. On attempt expiry it lazily waits for the
// caller to request a new URL (RequestNewURL) before charging again.
func (g *Gateway) RunPaymentLoop(ctx restate.WorkflowContext, p LoopParams) error {
	cancel := restate.Promise[struct{}](ctx, cancelKey)

	for attempt := 1; ; attempt++ {
		restate.Set(ctx, paymentAttemptKey, attempt)
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
			return p.ErrCancelled
		case sessionExp:
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
				return p.ErrCancelled
			case sessionExp2:
				return p.ErrExpired
			}
		}
	}
}

// charge calls the payment gateway for one attempt and persists the redirect
// URL on the tx. Returns "" for a zero-amount attempt.
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

// sessionExpiry returns a future that fires at the session deadline, or the
// terminal expired error if the deadline already passed.
func (g *Gateway) sessionExpiry(ctx restate.WorkflowContext, deadline time.Time, expired error) (restate.Future, error) {
	rem := time.Until(deadline)
	if rem <= 0 {
		return nil, expired
	}
	return restate.After(ctx, rem), nil
}
