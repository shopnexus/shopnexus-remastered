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

// CheckoutWorkflow drives the buyer checkout saga. It embeds the module core
// (*orderbase.Base) for the shared storage/notify/hydrate helpers and holds the
// shared payment gateway as a named field.
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

// Run orchestrates the checkout phases — price, reserve inventory, persist
// records, settle wallet + gateway — and owns the saga that unwinds them on a
// terminal failure.
func (h *CheckoutWorkflow) Run(
	ctx restate.WorkflowContext,
	input CheckoutWorkflowInput,
) (out CheckoutWorkflowOutput, err error) {
	defer metrics.TrackHandler("checkout_workflow", "Run", &err)()

	sessionID := uuid.MustParse(restate.Key(ctx))

	if err = validator.Validate(input); err != nil {
		return out, fmt.Errorf("validate checkout: %w", err)
	}
	if input.BuyNow && len(input.Items) != 1 {
		return out, ordermodel.ErrBuyNowSingleSkuOnly
	}

	saga := saga.New(ctx)
	defer func() {
		if restate.IsTerminalError(err) {
			saga.Compensate()
		}
	}()

	// Reject the synchronous WaitPaymentURL caller on a terminal failure.
	saga.Defer("reject_payment_url", func(_ restate.Context) error {
		return restate.Promise[string](ctx, "payment_url_1").Reject(err)
	})

	priced, err := h.price(ctx, input)
	if err != nil {
		return out, err
	}

	serialIDs, err := h.reserveInventory(ctx, saga, input, priced)
	if err != nil {
		return out, err
	}

	internalWalletAmount, err := h.persist(ctx, saga, input, priced, serialIDs, sessionID)
	if err != nil {
		return out, err
	}

	// The gateway leg is skipped for wallet-only carts.
	if gatewayAmount := priced.total - internalWalletAmount; gatewayAmount > 0 {
		if err = h.gw.RunPaymentLoop(ctx, gateway.LoopParams{
			SessionID:       sessionID,
			SessionDeadline: time.Now().Add(gateway.SessionExpiry),
			NotePrefix:      "checkout gateway payment",
			Description:     fmt.Sprintf("Checkout session %s", sessionID),
			PaymentOption:   input.PaymentOption,
			Amount:          gatewayAmount,
			Currency:        priced.buyerCurrency,
			ErrCancelled:    ordermodel.ErrCheckoutCancelled,
			ErrExpired:      ordermodel.ErrCheckoutExpired,
		}); err != nil {
			return out, fmt.Errorf("gateway payment loop: %w", err)
		}
	}

	// Success tail — clear the saga, then fan out side effects.
	saga.Clear()

	if err = h.trackPurchase(ctx, input); err != nil {
		return out, err
	}
	if err = h.notifySellers(ctx, input, priced); err != nil {
		return out, err
	}

	return CheckoutWorkflowOutput{Status: "paid", SessionID: sessionID}, nil
}

func (h *CheckoutWorkflow) trackPurchase(ctx restate.WorkflowContext, input CheckoutWorkflowInput) error {
	interactions := make([]analyticbiz.CreateInteraction, 0, len(input.Items))
	for _, item := range input.Items {
		interactions = append(interactions, analyticbiz.CreateInteraction{
			Account:   input.Account,
			EventType: analyticmodel.EventPurchase,
			RefType:   analyticmodel.InteractionRefTypeProduct,
			RefID:     item.SkuID.String(),
		})
	}
	if err := h.TrackInteractions(ctx, interactions...); err != nil {
		return fmt.Errorf("track purchase interactions: %w", err)
	}
	return nil
}

func (h *CheckoutWorkflow) notifySellers(ctx restate.WorkflowContext, input CheckoutWorkflowInput, priced pricing) error {
	sellerItems := make(map[uuid.UUID][]string)
	for _, item := range input.Items {
		spu := priced.spuMap[priced.skuMap[item.SkuID].SpuID]
		sellerItems[spu.AccountID] = append(sellerItems[spu.AccountID], spu.Name)
	}
	for sellerID, names := range sellerItems {
		summary := ordermodel.SummarizeNames(names)
		if err := h.Notify(ctx, accountbiz.CreateNotificationParams{
			AccountID: sellerID,
			Type:      accountmodel.NotiNewPendingItems,
			Channel:   accountmodel.ChannelInApp,
			Title:     "New pending items",
			Content:   fmt.Sprintf("New order for %s is waiting for your review.", summary),
		}); err != nil {
			return fmt.Errorf("notify seller: %w", err)
		}
	}
	return nil
}
