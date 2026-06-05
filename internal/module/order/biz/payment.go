package orderbiz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	commonbiz "shopnexus-server/internal/module/common/biz"
	orderdb "shopnexus-server/internal/module/order/db/sqlc"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/provider/payment"
	"shopnexus-server/internal/provider/payment/card"
	"shopnexus-server/internal/provider/payment/sepay"
	"shopnexus-server/internal/provider/payment/vnpay"
	sharedmodel "shopnexus-server/internal/shared/model"
	"shopnexus-server/internal/shared/validator"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/jackc/pgx/v5"
	restate "github.com/restatedev/sdk-go"
)

// OnPaymentResult is the unified entry point for gateway IPN webhooks.
func (b *paymentHandler) OnPaymentResult(ctx restate.Context, params payment.Notification) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate on payment result: %w", err)
	}

	txID, err := uuid.Parse(params.RefID)
	if err != nil {
		return fmt.Errorf("parse tx id: %w", err)
	}

	tx, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderTransaction, error) {
		return b.storage.Querier().GetTransaction(rctx, uuid.NullUUID{UUID: txID, Valid: true})
	})
	if err != nil {
		return fmt.Errorf("get transaction: %w", err)
	}

	// load session + resolve TxID if the webhook didn't supply one.
	session, err := restate.Run(ctx, func(rctx restate.RunContext) (orderdb.OrderPaymentSession, error) {
		return b.storage.Querier().GetPaymentSession(rctx, uuid.NullUUID{UUID: tx.SessionID, Valid: true})
	})
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	wfName, wfID := WorkflowForSession(session)
	if wfName == "" {
		return nil
	}

	// signal owning workflow's payment_event promise.
	restate.WorkflowSend(ctx, wfName, wfID, "PaymentNotification").Send(params)
	return nil
}

// WorkflowForSession maps payment_session.kind to (workflowName, workflowID).
func WorkflowForSession(s orderdb.OrderPaymentSession) (workflowName, workflowID string) {
	switch s.Kind {
	case ordermodel.SessionKindBuyerCheckout:
		return "CheckoutWorkflow", s.ID.String()
	case ordermodel.SessionKindSellerConfirmationFee:
		return "ConfirmWorkflow", s.ID.String()
	default:
		return "", ""
	}
}

type InitGatewayPaymentParams struct {
	TxID          uuid.UUID
	Amount        int64
	PaymentOption string
	Description   string
}

// InitGatewayPayment creates a gateway payment
func (b *paymentHandler) InitGatewayPayment(
	ctx restate.Context,
	params InitGatewayPaymentParams,
) (string, error) {
	if params.Amount <= 0 {
		return "", nil
	}

	paymentClient, err := b.getPaymentClient(params.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("get payment client: %w", err)
	}

	url, err := restate.Run(ctx, func(rctx restate.RunContext) (string, error) {
		r, e := paymentClient.Charge(rctx, payment.ChargeParams{
			RefID:       params.TxID.String(),
			Amount:      params.Amount,
			Description: params.Description,
		})
		if e != nil {
			return "", e
		}
		return r.RedirectURL, nil
	})
	if err != nil {
		return "", fmt.Errorf("create gateway payment: %w", err)
	}

	if url != "" {
		if pErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			data, _ := json.Marshal(map[string]string{"gateway_url": url})
			return b.storage.Querier().SetTransactionData(rctx, orderdb.SetTransactionDataParams{
				ID:   params.TxID,
				Data: data,
			})
		}); pErr != nil {
			return "", fmt.Errorf("persist gateway url on tx: %w", pErr)
		}
	}

	return url, nil
}

// GetReusableGatewayURL reports whether a checkout/confirm session has a
// Pending+not-expired gateway tx whose URL the client can reuse. The echo
// "ensure payment URL" handler uses this to skip a workflow round-trip on
// the happy path; on the retry path it falls back to RequestNewPaymentURL.
func (b *paymentHandler) GetReusableGatewayURL(
	ctx restate.Context,
	sessionID uuid.UUID,
) (ReusableGatewayURLState, error) {
	var state ReusableGatewayURLState

	session, err := b.storage.Querier().GetPaymentSession(ctx, uuid.NullUUID{UUID: sessionID, Valid: true})
	if err != nil {
		return state, fmt.Errorf("get payment session: %w", err)
	}
	if session.Status != orderdb.OrderStatusPending {
		state.SessionTerminated = true
		return state, nil
	}

	tx, err := b.storage.Querier().GetLatestGatewayTxBySession(ctx, sessionID)
	if err != nil {
		// pgx returns ErrNoRows when no gateway tx exists yet — treat as
		// "no reusable URL" so the caller signals the workflow.
		if errors.Is(err, pgx.ErrNoRows) {
			return state, nil
		}
		return state, fmt.Errorf("get latest gateway tx: %w", err)
	}

	if tx.Status == orderdb.OrderStatusPending &&
		tx.DateExpired.Valid &&
		tx.DateExpired.Time.After(time.Now()) {
		var data struct {
			GatewayURL string `json:"gateway_url"`
		}
		if jerr := json.Unmarshal(tx.Data, &data); jerr == nil && data.GatewayURL != "" {
			state.ReusableURL = data.GatewayURL
		}
	}
	return state, nil
}

// gatewayPaymentLoopParams configures runGatewayPaymentLoop for a specific
// workflow (CheckoutWorkflow vs ConfirmWorkflow). Differences are pure data:
// amounts, currencies, error mapping, log strings.
type gatewayPaymentLoopParams struct {
	SessionID       uuid.UUID
	WorkflowID      uuid.UUID
	SessionDeadline time.Time

	NotePrefix    string // tx.note prefix, e.g. "checkout gateway payment"
	Description   string // gateway transaction memo, e.g. "Checkout session %s"
	PaymentOption string
	Amount        int64
	Currency      string // Rail debit currency

	ErrCancelled error
	ErrExpired   error
}

// runGatewayPaymentLoop drives the multi-attempt gateway payment leg shared
// by CheckoutWorkflow and ConfirmWorkflow. Each iteration: mints a fresh
// gateway tx (Pending, expires now+paymentExpiry), calls InitGatewayPayment,
// resolves payment_url_<attempt>. Then waits on:
//
//   - paymentPromise (payment_event_<txID>): gateway settled. On Success,
//     marks the tx + session Success and returns nil.
//   - cancelPromise:  buyer/seller signalled cancel → terminal ErrCancelled.
//   - attempt-expiry: this attempt's URL window elapsed → mark tx Failed,
//     wait for retry_<attempt> (resolved by RequestNewPaymentURL shared
//     handler) and loop into the next attempt.
//   - session-expiry: overall session deadline elapsed → terminal ErrExpired.
//
// Caller is responsible for: workflow-level saga registration, the
// pre-loop session/wallet-tx creation, and the post-success tail. This
// helper only owns the gateway leg.
func (b *paymentHandler) runGatewayPaymentLoop(
	ctx restate.WorkflowContext,
	p gatewayPaymentLoopParams,
) error {
	cancelPromise := restate.Promise[struct{}](ctx, "user_cancel")
	var attempt int

paymentLoop:
	for {
		attempt++
		restate.Set(ctx, "payment_attempt", attempt)
		attemptTxID := restate.UUID(ctx)

		if cErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			_, e := b.storage.Querier().CreateDefaultTransaction(rctx, orderdb.CreateDefaultTransactionParams{
				ID:            attemptTxID,
				SessionID:     p.SessionID,
				Status:        orderdb.OrderStatusPending,
				Note:          fmt.Sprintf("%s (attempt %d)", p.NotePrefix, attempt),
				Error:         null.String{},
				PaymentOption: null.StringFrom(p.PaymentOption),
				Data:          json.RawMessage("{}"),
				Amount:        p.Amount,
				Currency:      p.Currency,
				ReversesID:    uuid.NullUUID{},
				DateSettled:   null.Time{},
				DateExpired:   null.TimeFrom(time.Now().Add(paymentExpiry)),
			})
			return e
		}); cErr != nil {
			return fmt.Errorf("db create gateway tx: %w", cErr)
		}

		url, gErr := b.InitGatewayPayment(ctx, InitGatewayPaymentParams{
			TxID:          attemptTxID,
			Amount:        p.Amount,
			PaymentOption: p.PaymentOption,
			Description:   fmt.Sprintf("%s (attempt %d)", p.Description, attempt),
		})
		if gErr != nil {
			return gErr
		}
		if pErr := restate.Promise[string](ctx, fmt.Sprintf("payment_url_%d", attempt)).Resolve(url); pErr != nil {
			return fmt.Errorf("resolve payment url promise: %w", pErr)
		}

		paymentPromise := restate.Promise[payment.Notification](ctx, "payment_event_"+attemptTxID.String())
		attemptExpiryFut := restate.After(ctx, paymentExpiry)
		sessionRem := time.Until(p.SessionDeadline)
		if sessionRem <= 0 {
			return p.ErrExpired
		}
		sessionExpiryFut := restate.After(ctx, sessionRem)

		done, werr := restate.WaitFirst(ctx, paymentPromise, cancelPromise, attemptExpiryFut, sessionExpiryFut)
		if werr != nil {
			return werr
		}
		switch done {
		case paymentPromise:
			ev, evErr := paymentPromise.Result()
			if evErr != nil {
				return fmt.Errorf("read payment event: %w", evErr)
			}
			switch ev.Status {
			case payment.StatusSuccess:
				// Promote this attempt's gateway tx + the session to Success.
				// Both queries guard on status='Pending' so they're idempotent
				// on workflow replay.
				if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
					now := time.Now()
					if _, e := b.storage.Querier().MarkTransactionSuccess(rctx, orderdb.MarkTransactionSuccessParams{
						ID:          attemptTxID,
						DateSettled: now,
					}); e != nil {
						return fmt.Errorf("mark gateway tx success: %w", e)
					}
					if _, e := b.storage.Querier().MarkPaymentSessionSuccess(rctx, orderdb.MarkPaymentSessionSuccessParams{
						ID:       p.SessionID,
						DatePaid: now,
					}); e != nil {
						return fmt.Errorf("mark payment session success: %w", e)
					}
					return nil
				}); mErr != nil {
					return mErr
				}
				break paymentLoop
			case payment.StatusFailed, payment.StatusExpired:
				return ordermodel.ErrPaymentFailed
			default:
				return fmt.Errorf("unknown payment event status: %w", ordermodel.ErrPaymentFailed)
			}
		case cancelPromise:
			return p.ErrCancelled
		case sessionExpiryFut:
			return p.ErrExpired
		case attemptExpiryFut:
			// This attempt's URL is dead. Mark its tx Failed and wait for the
			// caller to ask for a fresh one (RequestNewPaymentURL resolves
			// retry_<attempt>). Lazy retry: we don't burn gateway quota until
			// the user actually comes back.
			if mErr := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
				return b.storage.Querier().MarkTransactionsFailed(rctx, orderdb.MarkTransactionsFailedParams{
					ID:    []uuid.UUID{attemptTxID},
					Error: null.StringFrom("gateway attempt expired"),
				})
			}); mErr != nil {
				return fmt.Errorf("mark attempt failed: %w", mErr)
			}

			retryPromise := restate.Promise[struct{}](ctx, fmt.Sprintf("retry_%d", attempt))
			sessionRem2 := time.Until(p.SessionDeadline)
			if sessionRem2 <= 0 {
				return p.ErrExpired
			}
			sessionExpiryFut2 := restate.After(ctx, sessionRem2)

			done2, werr2 := restate.WaitFirst(ctx, retryPromise, cancelPromise, sessionExpiryFut2)
			if werr2 != nil {
				return werr2
			}
			switch done2 {
			case retryPromise:
				continue paymentLoop
			case cancelPromise:
				return p.ErrCancelled
			case sessionExpiryFut2:
				return p.ErrExpired
			}
		}
	}
	return nil
}

// SetupPaymentMap registers the payment options in the central catalog.
// Clients themselves are built on demand — nothing is cached on the handler.
func (b *paymentHandler) SetupPaymentMap() error {
	configs := b.paymentConfigs()

	go func() {
		if err := b.common.UpsertOptions(context.Background(), commonbiz.UpsertOptionsParams{
			Type:    string(sharedmodel.OptionTypePayment),
			Configs: configs,
		}); err != nil {
			b.logger.Warn("register payment options", slog.Any("error", err))
		}
	}()

	return nil
}

// paymentFactory routes a payment Option to its provider-specific constructor.
func (b *paymentHandler) paymentFactory(cfg sharedmodel.Option) payment.Client {
	switch cfg.Provider {
	case "vnpay":
		return vnpay.NewClient(cfg)
	case "sepay":
		return sepay.NewClient(cfg)
	case "card":
		return card.NewClient(cfg)
	default:
		b.logger.Warn("unknown payment provider", "provider", cfg.Provider, "id", cfg.ID)
		return nil
	}
}

func (b *paymentHandler) paymentConfigs() []sharedmodel.Option {
	var configs []sharedmodel.Option

	returnURL := b.cfg.Order.ReturnURL

	vnpayCfg := b.cfg.Vnpay
	for _, method := range []string{vnpay.MethodQR, vnpay.MethodBank, vnpay.MethodATM} {
		data, _ := json.Marshal(vnpay.Data{
			TmnCode:    vnpayCfg.TmnCode,
			HashSecret: vnpayCfg.HashSecret,
			ReturnURL:  returnURL,
			Method:     method,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "vnpay_" + method,
			Type:     sharedmodel.OptionTypePayment,
			Provider: "vnpay",
			Name:     "VNPay - " + method,
			Data:     data,
		})
	}

	if c := b.cfg.Sepay; c.MerchantID != "" {
		data, _ := json.Marshal(sepay.Data{
			MerchantID:    c.MerchantID,
			SecretKey:     c.SecretKey,
			IPNSecretKey:  c.IPNSecretKey,
			PublicBaseURL: c.PublicBaseURL,
			ReturnURL:     returnURL,
			Sandbox:       c.Sandbox,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "sepay_bank_transfer",
			Type:     sharedmodel.OptionTypePayment,
			Provider: "sepay",
			Name:     "SePay - Bank Transfer",
			Data:     data,
		})
	}

	if c := b.cfg.CardPayment; c.Provider != "" {
		data, _ := json.Marshal(card.Data{
			Processor: c.Provider,
			SecretKey: c.SecretKey,
			PublicKey: c.PublicKey,
		})
		configs = append(configs, sharedmodel.Option{
			ID:       "card_" + c.Provider,
			Type:     sharedmodel.OptionTypePayment,
			Provider: "card",
			Name:     "Card Payment (" + c.Provider + ")",
			Data:     data,
		})
	}

	return configs
}

// getPaymentClient builds a payment client on demand for the given option ID.
// The lookup walks the config-derived option list — no per-handler cache.
func (b *paymentHandler) getPaymentClient(option string) (payment.Client, error) {
	for _, cfg := range b.paymentConfigs() {
		if cfg.ID == option {
			if client := b.paymentFactory(cfg); client != nil {
				return client, nil
			}
			break
		}
	}
	return nil, ordermodel.ErrUnknownPaymentOption.Fmt(option)
}

// ReusableGatewayURLState reports the latest gateway-payment state for a
// payment_session. SessionTerminated=true means the session is in a final
// state (Cancelled/Failed/Success) — caller should 410 Gone. ReusableURL
// non-empty means there's a Pending+not-expired tx; reuse it. Both empty
// means caller should signal the workflow to spawn the next attempt.
type ReusableGatewayURLState struct {
	SessionTerminated bool   `json:"session_terminated"`
	ReusableURL       string `json:"reusable_url"`
}

const (
	// paymentExpiry bounds a single gateway payment attempt — i.e. how long the
	// user has to complete one VNPay/Momo redirect before that URL is considered
	// dead. Multi-attempt sessions can spawn another attempt (up to sessionExpiry).
	paymentExpiry = 30 * time.Minute
	// sessionExpiry bounds the entire checkout/confirm session across all retry
	// attempts. Once it elapses, the session is failed via saga regardless of
	// whether the buyer is mid-attempt.
	sessionExpiry = 24 * time.Hour
)
