# Order Module

Manages the full order lifecycle: cart, checkout, seller confirmation, payment, delivery, cancellation, and refunds. The most complex module in the system — orchestrates inventory, transport, payment, and promotion modules.

## ER Diagram

<!--START_SECTION:mermaid-->
```mermaid
erDiagram
"order.order" }o--|| "order.transport" : "transport_id"
"order.item" }o--|o "order.order" : "order_id"
"order.item" }o--|| "order.payment_session" : "payment_session_id"
"order.transaction" }o--|| "order.payment_session" : "session_id"
"order.transaction" }o--|o "order.transaction" : "reverses_id"
"order.refund" |o--|o "order.transport" : "return_to_buyer_transport_id"
"order.refund" |o--|| "order.transport" : "return_transport_id"
"order.refund" }o--|| "order.order" : "order_id"
"order.refund" }o--|o "order.transaction" : "refund_tx_id"
"order.refund_dispute" }o--|| "order.refund" : "refund_id"

"order.cart_item" {
  bigint id
  uuid account_id
  uuid sku_id
  bigint quantity
}
"order.item" {
  bigint id
  uuid order_id
  uuid account_id
  uuid seller_id
  uuid sku_id
  uuid spu_id
  text sku_name
  text address
  text note
  jsonb serial_ids
  bigint quantity
  text transport_option
  bigint subtotal_amount
  bigint total_amount
  varchar(3) source_currency
  uuid payment_session_id
  timestamptz date_cancelled
  uuid cancelled_by_id
  timestamptz date_created
}
"order.order" {
  uuid id
  uuid buyer_id
  uuid seller_id
  bigint transport_id
  text address
  timestamptz date_created
  uuid confirmed_by_id
  uuid confirm_session_id
  text note
}
"order.payment_session" {
  uuid id
  text kind
  status status
  uuid from_id
  uuid to_id
  text note
  varchar(3) currency
  bigint total_amount
  jsonb fx_snapshot
  jsonb data
  timestamptz date_created
  timestamptz date_paid
  timestamptz date_expired
}
"order.refund" {
  uuid id
  uuid account_id
  uuid order_id
  text reason
  jsonb attachments
  timestamptz date_created
  refund_status status
  bigint return_transport_id
  timestamptz date_received_by_seller
  timestamptz review_deadline
  timestamptz seller_decision_at
  bigint return_to_buyer_transport_id
  text rejection_reason
  uuid refund_tx_id
}
"order.refund_dispute" {
  uuid id
  uuid refund_id
  uuid account_id
  text reason
  jsonb attachments
  timestamptz date_created
  dispute_status status
  uuid resolved_by_id
  timestamptz date_resolved
  text resolution_note
}
"order.transaction" {
  uuid id
  uuid session_id
  status status
  text note
  text error
  text payment_option
  jsonb data
  bigint amount
  varchar(3) currency
  uuid reverses_id
  timestamptz date_created
  timestamptz date_settled
  timestamptz date_expired
}
"order.transport" {
  bigint id
  text option
  status status
  jsonb data
  timestamptz date_created
}
```
<!--END_SECTION:mermaid-->

## Domain Concepts

### Pending Items vs Orders

**Checkout creates pending items, not orders.** When a buyer checks out, the system reserves inventory and creates `order.item` records with `status = Pending` — but no `order.order` row exists yet. The seller then reviews incoming pending items and either confirms or rejects them.

**Sellers create orders** by confirming pending items via `ConfirmSellerPending`. This groups the confirmed items into an `order.order`, creates a transport record, and calculates the total cost (product cost - discount + transport cost). Only after confirmation does a proper "order" exist.

### Payment

Payment is separate from order creation. The `payment_id` on orders is nullable until the buyer calls `PayBuyerOrders`. A single payment can cover multiple orders. Payment providers are pluggable — each implements a `payment.Client` interface and is registered in `paymentMap` at startup.

### Transport

Transport providers are also pluggable via `transport.Client`. The seller selects a transport option when confirming pending items. The system calls `transportClient.Quote()` for cost estimation and `transportClient.Create()` when finalizing the order. Transport data (tracking ID, provider-specific metadata) is stored as JSONB.

### Refunds

Buyers commit physically when opening a refund: a return shipment is created at request time (mandatory transport option + evidence photos). The refund flows through 5 terminal-bounded states:

```
Shipping → AwaitingSellerReview → { Accepted | Disputed → { Accepted | Rejected } }
```

- **Shipping**: return transport in motion (mocked auto-Success in dev).
- **AwaitingSellerReview**: seller has 3 days to decide (auto-accept on timeout).
- **Disputed**: seller refused → admin reviews.
- **Accepted**: buyer wallet credited via reversal transaction.
- **Rejected**: admin sided with seller → items shipped back to buyer, no refund.

Disputes are **seller-initiated only**. Admin resolves with `Uphold` (SellerWins → refund Rejected) or `Dismiss` (BuyerWins → refund Accepted). The credit path is shared by `SellerApproveRefund`, `AutoAcceptRefund`, and `AdminDismissDispute` via the `executeRefundCredit` helper.

## Flows

### Order Lifecycle

```
Cart → Checkout (pending items) → Seller confirms (creates order + transport)
     → Buyer pays (PayBuyerOrders) → Delivery → (optional) Refund
```

1. **Cart**: buyer adds SKUs to cart (`UpdateCart`). Cart is a simple quantity map.
2. **Checkout** (`BuyerCheckout`): validates SKUs, reserves inventory via the inventory module, removes items from cart, creates pending `order.item` records. Supports "buy now" mode (skip cart).
3. **Buyer pending**: buyer lists (`ListBuyerPending`) and can cancel (`CancelBuyerPending`) pending items, which releases the reserved inventory.
4. **Seller incoming**: seller views pending items for their products (`ListSellerPending`). Can preview costs via `QuoteTransport` before confirming. `ConfirmSellerPending` groups items into an order, creates a transport, and calculates totals. `RejectSellerPending` releases inventory.
5. **Payment** (`PayBuyerOrders`): buyer pays one or more confirmed orders. Creates a payment record, calls the payment provider. Provider webhook confirms success/failure.
6. **Refund** (v2): buyer ships return at `CreateBuyerRefund` time. `RefundWorkflow` (Restate, keyed by refund ID) drives the 3 phases: wait Delivered (14D timeout fallback) → wait Seller decision (3D auto-accept) → wait Admin decision. Seller can `SellerApproveRefund` or `SellerDisputeRefund`. Admin resolves disputes via `AdminUpholdDispute` / `AdminDismissDispute`.

### Payment Webhook Flow

1. `PayBuyerOrders` creates a payment record and redirects buyer to the provider's payment page.
2. Provider processes payment and calls the webhook (mounted dynamically via `payment.Client.MountWebhookRoutes()`).
3. `ConfirmPayment` updates the payment status and all associated orders.

## Implementation Notes

- **Pluggable provider maps**: `paymentMap` and `transportMap` are `map[string]payment.Client` and `map[string]transport.Client`, populated at startup from config. The provider string on each order/transport record is the map key.
- **Transport quote**: `QuoteTransport` calls `transportClient.Quote()` inside a `restate.Run()` closure for durability. This gives sellers a cost preview before confirming, including product cost, discount, transport cost, and total.
- **`restate.Run()` for side effects**: transport creation and payment provider calls are wrapped in `restate.Run()` to ensure they're journaled. If the process crashes after creating a transport but before updating the order, Restate replays and skips the already-completed transport creation.
- **Inventory coordination**: checkout reserves inventory via `inventory.ReserveInventory`. Cancellation (buyer cancel, seller reject) releases via `inventory.ReleaseInventory`. Both are cross-module Restate calls — durable and exactly-once.
- **Promotion integration**: during checkout, prices are calculated via `promotion.CalculatePromotedPrices`, which applies all applicable discounts and returns the effective price per SKU.

## Endpoints

All under `/api/v1/order`.

### Cart

| Method | Path | Description |
|--------|------|-------------|
| GET | `/cart` | List cart items |
| POST | `/cart` | Add/update/remove cart item |
| DELETE | `/cart` | Remove all cart items |

### Buyer — Pending

| Method | Path | Description |
|--------|------|-------------|
| POST | `/buyer/checkout` | Checkout items, reserve inventory, create pending items |
| GET | `/buyer/pending` | List buyer's pending items (filterable by `status`) |
| DELETE | `/buyer/pending/:id` | Cancel a pending item (releases inventory) |

### Buyer — Confirmed

| Method | Path | Description |
|--------|------|-------------|
| GET | `/buyer/confirmed` | List buyer's orders (filterable by `status`) |
| GET | `/buyer/confirmed/:id` | Get order by ID |
| DELETE | `/buyer/confirmed/:id` | Cancel unpaid order (releases inventory) |
| POST | `/buyer/pay` | Pay for confirmed orders |

### Buyer — Refund

| Method | Path | Description |
|--------|------|-------------|
| GET | `/buyer/refund` | List refund requests |
| POST | `/buyer/refund` | Create refund + commit return shipment (mandatory evidence photos) |

### Seller — Pending

| Method | Path | Description |
|--------|------|-------------|
| GET | `/seller/pending` | List pending items for seller's products |
| POST | `/seller/pending/quote` | Preview transport cost before confirming |
| POST | `/seller/pending/confirm` | Confirm items, create transport + order |
| POST | `/seller/pending/reject` | Reject pending items (releases inventory) |

### Seller — Confirmed

| Method | Path | Description |
|--------|------|-------------|
| GET | `/seller/confirmed` | List seller's orders (filterable by `search`, `status`) |
| GET | `/seller/confirmed/:id` | Get order by ID |

### Seller — Refund

| Method | Path | Description |
|--------|------|-------------|
| GET | `/seller/refund` | List refund requests against seller's orders |
| POST | `/refunds/:id/approve` | Seller approves → credit buyer wallet |
| POST | `/refunds/:id/dispute` | Seller disputes → escalate to admin (mandatory evidence) |

### Disputes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/disputes` | List disputes (admin: all; buyer/seller: own) |
| GET | `/disputes/:id` | Get dispute detail |
| GET | `/refunds/:refundID/disputes` | List disputes for a specific refund |
| POST | `/disputes/:disputeID/uphold` | **Admin only**. Seller wins → refund Rejected |
| POST | `/disputes/:disputeID/dismiss` | **Admin only**. Buyer wins → refund Accepted |

### Payment Webhooks

Payment providers register webhook routes at startup (e.g., VNPay IPN). These are mounted dynamically via `payment.Client.MountWebhookRoutes()`.

## Providers

**Payment**: VNPay (QR/Bank/ATM), COD (`system-cod`)

**Transport**: GHTK (Express/Standard/Economy) — mock implementation with cost based on weight and service tier

## Cross-Module Dependencies

| Module | Usage |
|--------|-------|
| `account` | Authenticated identity, seller default contacts for shipping origin, notifications |
| `catalog` | SPU/SKU lookup, pricing, package details for transport quoting |
| `inventory` | Reserve/release inventory during checkout and cancellation |
| `promotion` | Price calculation with active promotions and discount codes |
| `common` | Resource management for refund images |
