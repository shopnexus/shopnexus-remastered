package checkout

import (
	"fmt"
	"time"

	restate "github.com/restatedev/sdk-go"

	"shopnexus-server/internal/infras/metrics"
	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderbase "shopnexus-server/internal/module/order/biz/base"
	"shopnexus-server/internal/module/order/biz/workflow/gateway"
	ordermodel "shopnexus-server/internal/module/order/model"
	"shopnexus-server/internal/shared/saga"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
)

// CheckoutWorkflow holds shared, immutable dependencies; per-invocation state lives on checkoutRun.
type CheckoutWorkflow struct {
	*orderbase.Base

	gw        *gateway.Gateway
	account   accountbiz.AccountBizClient
	catalog   catalogbiz.CatalogBizClient
	common    commonbiz.CommonBizClient
	inventory inventorybiz.InventoryBizClient
}

func New(
	core *orderbase.Base,
	gw *gateway.Gateway,
	account accountbiz.AccountBizClient,
	catalog catalogbiz.CatalogBizClient,
	common commonbiz.CommonBizClient,
	inventory inventorybiz.InventoryBizClient,
) *CheckoutWorkflow {
	return &CheckoutWorkflow{core, gw, account, catalog, common, inventory}
}

func (h *CheckoutWorkflow) ServiceName() string { return "CheckoutWorkflow" }

// checkoutRun is the per-invocation scope; phases share state via its fields.
type checkoutRun struct {
	*CheckoutWorkflow
	ctx       restate.WorkflowContext
	saga      *saga.Saga
	input     CheckoutWorkflowInput
	sessionID uuid.UUID

	pricing                                     // set by price()
	serialIDs            map[uuid.UUID][]string // set by reserve()
	internalWalletAmount int64                  // set by persist()
}

// Run orchestrates the checkout phases and owns the saga that unwinds on terminal failure.
func (h *CheckoutWorkflow) Run(
	ctx restate.WorkflowContext,
	input CheckoutWorkflowInput,
) (out CheckoutWorkflowOutput, err error) {
	defer metrics.TrackHandler("checkout_workflow", "Run", &err)()

	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate checkout: %w", err)
	}
	if input.BuyNow && len(input.Items) != 1 {
		return out, ordermodel.ErrBuyNowSingleSkuOnly
	}

	r := &checkoutRun{
		CheckoutWorkflow: h,
		ctx:              ctx,
		saga:             saga.New(ctx),
		input:            input,
		sessionID:        uuid.MustParse(restate.Key(ctx)),
	}
	defer func() {
		if restate.IsTerminalError(err) {
			r.saga.Compensate()
		}
	}()

	// Unblock any synchronous GetPaymentURL caller on terminal failure.
	r.saga.Defer("reject_payment_url", func(_ restate.Context) error {
		return r.gw.RejectPendingURLs(ctx, err)
	})

	// decision: price the cart
	if err = r.price(); err != nil {
		return out, err
	}
	// execution: reserve, persist, settle payment
	if err = r.reserve(); err != nil {
		return out, err
	}
	if err = r.persist(); err != nil {
		return out, err
	}
	if err = r.pay(); err != nil {
		return out, err
	}

	// tail: fan-out side effects
	r.saga.Clear()
	if err = r.trackPurchase(); err != nil {
		return out, err
	}
	if err = r.notifySellers(); err != nil {
		return out, err
	}

	return CheckoutWorkflowOutput{Status: "paid", SessionID: r.sessionID}, nil
}

// pay runs the gateway leg; skipped for wallet-only carts.
func (r *checkoutRun) pay() error {
	gatewayAmount := r.total - r.internalWalletAmount
	if gatewayAmount <= 0 {
		return nil
	}
	if err := r.gw.RunPaymentLoop(r.ctx, gateway.LoopParams{
		SessionID:       r.sessionID,
		SessionDeadline: time.Now().Add(gateway.SessionExpiry),
		NotePrefix:      "checkout gateway payment",
		Description:     fmt.Sprintf("Checkout session %s", r.sessionID),
		PaymentOption:   r.input.PaymentOption,
		Amount:          gatewayAmount,
		Currency:        r.buyerCurrency,
		ErrCancelled:    ordermodel.ErrCheckoutCancelled,
		ErrExpired:      ordermodel.ErrCheckoutExpired,
	}); err != nil {
		return fmt.Errorf("gateway payment loop: %w", err)
	}
	return nil
}

func (r *checkoutRun) trackPurchase() error {
	interactions := make([]analyticbiz.CreateInteraction, 0, len(r.input.Items))
	for _, item := range r.input.Items {
		interactions = append(interactions, analyticbiz.CreateInteraction{
			Account:   r.input.Account,
			EventType: analyticmodel.EventPurchase,
			RefType:   analyticmodel.InteractionRefTypeProduct,
			RefID:     item.SkuID.String(),
		})
	}
	if err := r.TrackInteractions(r.ctx, interactions...); err != nil {
		return fmt.Errorf("track purchase interactions: %w", err)
	}
	return nil
}

func (r *checkoutRun) notifySellers() error {
	sellerItems := make(map[uuid.UUID][]string)
	for _, item := range r.input.Items {
		spu := r.spuMap[r.skuMap[item.SkuID].SpuID]
		sellerItems[spu.AccountID] = append(sellerItems[spu.AccountID], spu.Name)
	}
	for sellerID, names := range sellerItems {
		if err := r.Notify(r.ctx, accountbiz.CreateNotificationParams{
			AccountID: sellerID,
			Type:      accountmodel.NotiNewPendingItems,
			Channel:   accountmodel.ChannelInApp,
			Title:     "New pending items",
			Content:   fmt.Sprintf("New order for %s is waiting for your review.", ordermodel.SummarizeNames(names)),
		}); err != nil {
			return fmt.Errorf("notify seller: %w", err)
		}
	}
	return nil
}
