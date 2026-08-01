// Package gateway builds the ServeMux + middleware and routes to handlers.
package gateway

import (
	"log/slog"
	"net/http"

	openapi "shopnexus/api"
	"shopnexus/internal/gateway/handler"
	"shopnexus/internal/gateway/middleware"
	"shopnexus/internal/module/observability"
	"shopnexus/internal/provider/storage/local"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
)

type Deps struct {
	// Webhooks is where providers mount their own IPN paths. It is outside the versioned
	// base path and outside auth: a gateway calls the URL it was configured with, signs
	// the payload its own way, and has no bearer token to present.
	Webhooks *http.ServeMux
	Account  *handler.Account
	Catalog  *handler.Catalog
	Chat     *handler.Chat
	Finance  *handler.Finance
	Order    *handler.Order
	Trust    *handler.Trust
	// Uploads is nil unless the storage backend needs this process to serve the bytes — the
	// `local` one does, a real bucket does not.
	Uploads  *handler.Uploads
	Metrics  *observability.Sink
	Tokens   *token.Manager
	Sessions *session.Store
	Log      *slog.Logger
}

// NewRouter registers every route the OpenAPI contract declares.
//
// Routes are written out by hand rather than derived from the spec at startup: a
// router generated from the document would satisfy
// TestOpenAPIContract_AllPathsRouted by construction and stop being able to catch
// a documented endpoint nobody wired up.
//
// A route reachable without a token is under "Public"; everything else is wrapped
// in the auth middleware, which mirrors the "security" field of the operation in
// the spec. An operation that lists both `{}` and `bearerAuth` gets OptionalAuth:
// anonymous is allowed, but a token that is present is honoured. Role checks —
// moderator or admin on the /admin routes — are the handler's job, since the
// middleware only establishes who the caller is.
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	auth := middleware.Auth(d.Tokens, d.Sessions, d.Log)
	optionalAuth := middleware.OptionalAuth(d.Tokens, d.Sessions, d.Log)

	// The object route the `local` storage backend signs against. Registered only when that
	// backend is in use, and deliberately outside auth: the signature names the method, the key
	// and an expiry, so it is the authorization — a bearer token here would stop a client
	// handing the URL to the thing that actually uploads.
	if d.Uploads != nil {
		mux.HandleFunc("PUT "+local.ObjectPath, d.Uploads.Put)
		mux.HandleFunc("GET "+local.ObjectPath, d.Uploads.Get)
	}

	// API docs (OpenAPI spec + Swagger UI)
	mux.HandleFunc("GET /openapi.yaml", openapi.SpecHandler)
	mux.HandleFunc("GET /docs", openapi.DocsHandler)

	// ---- account ----
	// Public
	mux.HandleFunc("POST /register", d.Account.Register)
	mux.HandleFunc("POST /login", d.Account.Login)
	mux.HandleFunc("POST /login/oauth", d.Account.LoginOAuth)
	mux.HandleFunc("POST /token/refresh", d.Account.RefreshToken)
	mux.HandleFunc("POST /password/reset-requests", d.Account.RequestPasswordReset)
	mux.HandleFunc("POST /password/resets", d.Account.ResetPassword)
	mux.HandleFunc("POST /email/verifications", d.Account.VerifyEmail)
	mux.HandleFunc("GET /accounts/{id}", d.Account.GetPublicAccount)
	mux.HandleFunc("GET /accounts/{id}/followers", d.Account.ListFollowers)
	// Authenticated
	mux.Handle("POST /logout", auth(http.HandlerFunc(d.Account.Logout)))
	mux.Handle("PUT /password", auth(http.HandlerFunc(d.Account.ChangePassword)))
	mux.Handle("POST /email/verification-requests", auth(http.HandlerFunc(d.Account.RequestEmailVerification)))
	mux.Handle("GET /me", auth(http.HandlerFunc(d.Account.GetMe)))
	mux.Handle("PATCH /me", auth(http.HandlerFunc(d.Account.UpdateMe)))
	mux.Handle("PATCH /me/profile", auth(http.HandlerFunc(d.Account.UpdateProfile)))
	mux.Handle("POST /me/uploads", auth(http.HandlerFunc(d.Account.CreateUpload)))
	mux.Handle("POST /me/uploads/{id}/confirmation", auth(http.HandlerFunc(d.Account.ConfirmUpload)))
	mux.Handle("GET /me/oauth-identities", auth(http.HandlerFunc(d.Account.ListOAuthIdentities)))
	mux.Handle("DELETE /me/oauth-identities/{provider}", auth(http.HandlerFunc(d.Account.UnlinkOAuthIdentity)))
	mux.Handle("GET /contacts", auth(http.HandlerFunc(d.Account.ListContacts)))
	mux.Handle("POST /contacts", auth(http.HandlerFunc(d.Account.CreateContact)))
	mux.Handle("PATCH /contacts/{id}", auth(http.HandlerFunc(d.Account.UpdateContact)))
	mux.Handle("DELETE /contacts/{id}", auth(http.HandlerFunc(d.Account.DeleteContact)))
	mux.Handle("POST /contacts/{id}/phone/verification-requests", auth(http.HandlerFunc(d.Account.RequestContactPhoneVerification)))
	mux.Handle("POST /contacts/{id}/phone/verifications", auth(http.HandlerFunc(d.Account.VerifyContactPhone)))
	mux.Handle("PUT /devices", auth(http.HandlerFunc(d.Account.RegisterDevice)))
	mux.Handle("GET /me/devices", auth(http.HandlerFunc(d.Account.ListDevices)))
	mux.Handle("DELETE /devices/{id}", auth(http.HandlerFunc(d.Account.DeleteDevice)))
	mux.Handle("GET /notifications", auth(http.HandlerFunc(d.Account.ListNotifications)))
	mux.Handle("GET /notifications/unread-count", auth(http.HandlerFunc(d.Account.GetNotificationUnreadCount)))
	mux.Handle("POST /notifications/read", auth(http.HandlerFunc(d.Account.MarkNotificationsRead)))
	mux.Handle("GET /notification-preferences", auth(http.HandlerFunc(d.Account.GetNotificationPreferences)))
	mux.Handle("PUT /notification-preferences", auth(http.HandlerFunc(d.Account.UpdateNotificationPreferences)))
	mux.Handle("GET /me/following", auth(http.HandlerFunc(d.Account.ListFollowing)))
	mux.Handle("PUT /follows/{accountID}", auth(http.HandlerFunc(d.Account.Follow)))
	mux.Handle("DELETE /follows/{accountID}", auth(http.HandlerFunc(d.Account.Unfollow)))
	mux.Handle("POST /identity-documents", auth(http.HandlerFunc(d.Account.StartIdentityVerification)))
	mux.Handle("GET /me/identity-documents", auth(http.HandlerFunc(d.Account.ListIdentityDocuments)))
	mux.Handle("GET /admin/accounts", auth(http.HandlerFunc(d.Account.AdminListAccounts)))
	mux.Handle("POST /admin/accounts/{id}/suspension", auth(http.HandlerFunc(d.Account.AdminSuspendAccount)))
	mux.Handle("DELETE /admin/accounts/{id}/suspension", auth(http.HandlerFunc(d.Account.AdminLiftSuspension)))
	mux.Handle("POST /admin/moderators", auth(http.HandlerFunc(d.Account.AdminCreateModerator)))
	mux.Handle("DELETE /admin/moderators/{id}", auth(http.HandlerFunc(d.Account.AdminRevokeModerator)))
	mux.Handle("GET /admin/identity-documents", auth(http.HandlerFunc(d.Account.AdminListIdentityDocuments)))
	mux.Handle("POST /admin/identity-documents/{id}/verdict", auth(http.HandlerFunc(d.Account.AdminRecordIdentityVerdict)))

	// ---- catalog ----
	// Public
	mux.HandleFunc("GET /categories", d.Catalog.ListCategories)
	mux.HandleFunc("GET /tags", d.Catalog.ListTags)
	// Public, wider for a known caller: the listing feed can be personalised or scoped
	// to the caller's own drafts, and a draft is readable by its owner.
	mux.Handle("GET /listings", optionalAuth(http.HandlerFunc(d.Catalog.ListListings)))
	mux.Handle("GET /listings/{id}", optionalAuth(http.HandlerFunc(d.Catalog.GetListing)))
	// Authenticated
	mux.Handle("POST /listings", auth(http.HandlerFunc(d.Catalog.CreateListing)))
	mux.Handle("PATCH /listings/{id}", auth(http.HandlerFunc(d.Catalog.UpdateListing)))
	mux.Handle("DELETE /listings/{id}", auth(http.HandlerFunc(d.Catalog.DeleteListing)))
	mux.Handle("POST /listings/{id}/publication", auth(http.HandlerFunc(d.Catalog.PublishListing)))
	mux.Handle("DELETE /listings/{id}/publication", auth(http.HandlerFunc(d.Catalog.HideListing)))
	mux.Handle("POST /listings/{id}/variants", auth(http.HandlerFunc(d.Catalog.CreateVariant)))
	mux.Handle("PATCH /variants/{id}", auth(http.HandlerFunc(d.Catalog.UpdateVariant)))
	mux.Handle("DELETE /variants/{id}", auth(http.HandlerFunc(d.Catalog.DeleteVariant)))
	mux.Handle("PUT /favorites/{listingID}", auth(http.HandlerFunc(d.Catalog.AddFavorite)))
	mux.Handle("DELETE /favorites/{listingID}", auth(http.HandlerFunc(d.Catalog.RemoveFavorite)))
	mux.Handle("POST /admin/categories", auth(http.HandlerFunc(d.Catalog.AdminCreateCategory)))
	mux.Handle("PATCH /admin/categories/{id}", auth(http.HandlerFunc(d.Catalog.AdminUpdateCategory)))
	mux.Handle("DELETE /admin/categories/{id}", auth(http.HandlerFunc(d.Catalog.AdminDeleteCategory)))
	mux.Handle("PUT /admin/tags/{slug}", auth(http.HandlerFunc(d.Catalog.AdminPutTag)))
	mux.Handle("DELETE /admin/tags/{slug}", auth(http.HandlerFunc(d.Catalog.AdminDeleteTag)))
	mux.Handle("GET /admin/listings", auth(http.HandlerFunc(d.Catalog.AdminListListings)))
	mux.Handle("POST /admin/listings/{id}/approval", auth(http.HandlerFunc(d.Catalog.AdminApproveListing)))
	mux.Handle("POST /admin/listings/{id}/takedown", auth(http.HandlerFunc(d.Catalog.AdminTakedownListing)))

	// ---- chat ----
	// Authenticated
	mux.Handle("POST /conversations/uploads", auth(http.HandlerFunc(d.Chat.CreateUpload)))
	mux.Handle("POST /conversations/uploads/{id}/confirmation", auth(http.HandlerFunc(d.Chat.ConfirmUpload)))
	mux.Handle("GET /conversations", auth(http.HandlerFunc(d.Chat.ListConversations)))
	mux.Handle("POST /conversations", auth(http.HandlerFunc(d.Chat.OpenConversation)))
	mux.Handle("GET /conversations/unread-count", auth(http.HandlerFunc(d.Chat.GetUnreadCount)))
	mux.Handle("GET /conversations/{id}", auth(http.HandlerFunc(d.Chat.GetConversation)))
	mux.Handle("GET /conversations/{id}/messages", auth(http.HandlerFunc(d.Chat.ListMessages)))
	mux.Handle("POST /conversations/{id}/messages", auth(http.HandlerFunc(d.Chat.SendMessage)))
	mux.Handle("POST /conversations/{id}/read", auth(http.HandlerFunc(d.Chat.MarkConversationRead)))
	mux.Handle("PATCH /messages/{id}", auth(http.HandlerFunc(d.Chat.UpdateMessage)))
	mux.Handle("DELETE /messages/{id}", auth(http.HandlerFunc(d.Chat.RedactMessage)))

	// No /resources or /options routes: the resource and option tables are now per module
	// (shared DDL, one copy per schema), so there is no module-agnostic place for an upload to
	// land. Each module grows its own upload route when it implements that flow.

	// ---- finance ----
	// Authenticated
	mux.Handle("GET /payment-sessions", auth(http.HandlerFunc(d.Finance.ListPaymentSessions)))
	mux.Handle("GET /payment-sessions/{id}", auth(http.HandlerFunc(d.Finance.GetPaymentSession)))
	mux.Handle("GET /payment-sessions/{id}/transactions", auth(http.HandlerFunc(d.Finance.ListTransactions)))
	mux.Handle("POST /payment-sessions/{id}/payments", auth(http.HandlerFunc(d.Finance.StartPayment)))
	mux.Handle("POST /payment-sessions/{id}/cancellation", auth(http.HandlerFunc(d.Finance.CancelPaymentSession)))
	mux.Handle("GET /wallets", auth(http.HandlerFunc(d.Finance.ListWallets)))
	mux.Handle("GET /wallets/{currency}", auth(http.HandlerFunc(d.Finance.GetWallet)))
	mux.Handle("GET /wallets/{currency}/transactions", auth(http.HandlerFunc(d.Finance.ListWalletTransactions)))
	mux.Handle("POST /withdrawals", auth(http.HandlerFunc(d.Finance.CreateWithdrawal)))
	mux.Handle("GET /withdrawals", auth(http.HandlerFunc(d.Finance.ListWithdrawals)))
	mux.Handle("GET /withdrawals/{id}", auth(http.HandlerFunc(d.Finance.GetWithdrawal)))
	mux.Handle("DELETE /withdrawals/{id}", auth(http.HandlerFunc(d.Finance.CancelWithdrawal)))
	mux.Handle("GET /bank-accounts", auth(http.HandlerFunc(d.Finance.ListBankAccounts)))
	mux.Handle("POST /bank-accounts", auth(http.HandlerFunc(d.Finance.CreateBankAccount)))
	mux.Handle("PATCH /bank-accounts/{id}", auth(http.HandlerFunc(d.Finance.UpdateBankAccount)))
	mux.Handle("DELETE /bank-accounts/{id}", auth(http.HandlerFunc(d.Finance.DeleteBankAccount)))
	mux.Handle("GET /tax-info", auth(http.HandlerFunc(d.Finance.GetTaxInfo)))
	mux.Handle("PUT /tax-info", auth(http.HandlerFunc(d.Finance.UpsertTaxInfo)))
	mux.Handle("GET /admin/withdrawals", auth(http.HandlerFunc(d.Finance.AdminListWithdrawals)))
	mux.Handle("POST /admin/withdrawals/{id}/approval", auth(http.HandlerFunc(d.Finance.AdminApproveWithdrawal)))
	mux.Handle("POST /admin/withdrawals/{id}/rejection", auth(http.HandlerFunc(d.Finance.AdminRejectWithdrawal)))
	mux.Handle("GET /admin/payment-sessions", auth(http.HandlerFunc(d.Finance.AdminListPaymentSessions)))
	mux.Handle("GET /admin/wallets/{accountID}", auth(http.HandlerFunc(d.Finance.AdminGetWallets)))
	mux.Handle("POST /admin/wallets/{accountID}/adjustments", auth(http.HandlerFunc(d.Finance.AdminAdjustWallet)))
	mux.Handle("POST /admin/tax-info/{accountID}/verification", auth(http.HandlerFunc(d.Finance.AdminVerifyTaxInfo)))

	// ---- order ----
	// Authenticated
	mux.Handle("GET /cart-items", auth(http.HandlerFunc(d.Order.ListCartItems)))
	mux.Handle("POST /cart-items", auth(http.HandlerFunc(d.Order.AddCartItem)))
	mux.Handle("PATCH /cart-items/{id}", auth(http.HandlerFunc(d.Order.UpdateCartItem)))
	mux.Handle("DELETE /cart-items/{id}", auth(http.HandlerFunc(d.Order.DeleteCartItem)))
	mux.Handle("POST /drafts", auth(http.HandlerFunc(d.Order.CreateDraft)))
	mux.Handle("GET /drafts", auth(http.HandlerFunc(d.Order.ListDrafts)))
	mux.Handle("GET /drafts/{id}", auth(http.HandlerFunc(d.Order.GetDraft)))
	mux.Handle("DELETE /drafts/{id}", auth(http.HandlerFunc(d.Order.CancelDraft)))
	mux.Handle("POST /drafts/{id}/checkout", auth(http.HandlerFunc(d.Order.Checkout)))
	mux.Handle("GET /items", auth(http.HandlerFunc(d.Order.ListItems)))
	mux.Handle("POST /items/{id}/cancellation", auth(http.HandlerFunc(d.Order.CancelItem)))
	mux.Handle("GET /orders", auth(http.HandlerFunc(d.Order.ListOrders)))
	mux.Handle("POST /orders/uploads", auth(http.HandlerFunc(d.Order.CreateUpload)))
	mux.Handle("POST /orders/uploads/{id}/confirmation", auth(http.HandlerFunc(d.Order.ConfirmUpload)))
	mux.Handle("GET /orders/{id}", auth(http.HandlerFunc(d.Order.GetOrder)))
	mux.Handle("POST /orders/{id}/receipt", auth(http.HandlerFunc(d.Order.ConfirmReceipt)))
	mux.Handle("POST /orders/{id}/cancellation", auth(http.HandlerFunc(d.Order.CancelOrder)))
	mux.Handle("GET /orders/{id}/transport", auth(http.HandlerFunc(d.Order.GetOrderTransport)))
	mux.Handle("POST /orders/{id}/transport/checkpoints", auth(http.HandlerFunc(d.Order.AdvanceShipment)))
	mux.Handle("POST /orders/{id}/refunds", auth(http.HandlerFunc(d.Order.CreateRefund)))
	mux.Handle("POST /offers", auth(http.HandlerFunc(d.Order.CreateOffer)))
	mux.Handle("GET /offers", auth(http.HandlerFunc(d.Order.ListOffers)))
	mux.Handle("GET /offers/{id}", auth(http.HandlerFunc(d.Order.GetOffer)))
	mux.Handle("PATCH /offers/{id}", auth(http.HandlerFunc(d.Order.CounterOffer)))
	mux.Handle("DELETE /offers/{id}", auth(http.HandlerFunc(d.Order.CancelOffer)))
	// What delivery costs, per carrier — for a draft or for agreed terms. One route, because the
	// buyer pays carriage on both kinds of sale and chooses it the same way.
	mux.Handle("POST /shipping-quotes", auth(http.HandlerFunc(d.Order.ShippingQuotes)))
	// Agreeing and buying are two steps: either party may agree to the price on the table, and
	// then the buyer turns it into an order — choosing delivery and paying — in the same checkout
	// a fixed-price listing uses.
	mux.Handle("POST /offers/{id}/acceptance", auth(http.HandlerFunc(d.Order.AcceptOffer)))
	mux.Handle("POST /offers/{id}/checkout", auth(http.HandlerFunc(d.Order.CheckoutOffer)))
	mux.Handle("GET /refunds", auth(http.HandlerFunc(d.Order.ListRefunds)))
	mux.Handle("GET /refunds/{id}", auth(http.HandlerFunc(d.Order.GetRefund)))
	mux.Handle("DELETE /refunds/{id}", auth(http.HandlerFunc(d.Order.WithdrawRefund)))
	mux.Handle("POST /refunds/{id}/attachments", auth(http.HandlerFunc(d.Order.AddRefundAttachments)))
	mux.Handle("POST /refunds/{id}/acceptance", auth(http.HandlerFunc(d.Order.AcceptRefund)))
	mux.Handle("POST /refunds/{id}/rejection", auth(http.HandlerFunc(d.Order.RejectRefund)))
	mux.Handle("POST /refunds/{id}/return-transport/checkpoints", auth(http.HandlerFunc(d.Order.AdvanceReturnShipment)))
	mux.Handle("POST /refunds/{id}/dispute", auth(http.HandlerFunc(d.Order.OpenDispute)))
	mux.Handle("GET /admin/disputes", auth(http.HandlerFunc(d.Order.AdminListDisputes)))
	mux.Handle("POST /admin/disputes/{id}/ruling", auth(http.HandlerFunc(d.Order.AdminRuleDispute)))

	// A photo is uploaded in two steps: a slot, then a confirmation once the bytes are at the
	// store. Both are the seller's, which is why they sit with the listing routes rather than in
	// a module-agnostic place — the upload belongs to the module that took it.
	mux.Handle("POST /listings/uploads", auth(http.HandlerFunc(d.Catalog.CreateUpload)))
	mux.Handle("POST /listings/uploads/{id}/confirmation", auth(http.HandlerFunc(d.Catalog.ConfirmUpload)))

	// ---- trust ----
	// Public
	mux.HandleFunc("GET /accounts/{accountID}/feedback", d.Trust.ListAccountFeedback)
	mux.HandleFunc("GET /accounts/{accountID}/reputation", d.Trust.GetReputation)
	// Reading a review is public, but a signed-in caller also gets their own vote back on
	// each row, which is what optionalAuth is for.
	mux.Handle("GET /listings/{listingID}/reviews", optionalAuth(http.HandlerFunc(d.Trust.ListReviews)))
	mux.Handle("GET /reviews/{id}", optionalAuth(http.HandlerFunc(d.Trust.GetReview)))
	// Authenticated
	mux.Handle("GET /orders/{orderID}/feedback", auth(http.HandlerFunc(d.Trust.GetOrderFeedback)))
	mux.Handle("POST /orders/{orderID}/feedback", auth(http.HandlerFunc(d.Trust.SubmitFeedback)))
	mux.Handle("POST /listings/{listingID}/reviews", auth(http.HandlerFunc(d.Trust.SubmitReview)))
	mux.Handle("PATCH /reviews/{id}", auth(http.HandlerFunc(d.Trust.UpdateReview)))
	mux.Handle("DELETE /reviews/{id}", auth(http.HandlerFunc(d.Trust.DeleteReview)))
	mux.Handle("POST /reviews/{id}/replies", auth(http.HandlerFunc(d.Trust.SubmitReviewReply)))
	mux.Handle("DELETE /review-replies/{id}", auth(http.HandlerFunc(d.Trust.DeleteReviewReply)))
	mux.Handle("PUT /reviews/{id}/vote", auth(http.HandlerFunc(d.Trust.VoteReview)))
	mux.Handle("DELETE /reviews/{id}/vote", auth(http.HandlerFunc(d.Trust.UnvoteReview)))
	// A review photo, in two steps — same shape as catalog's listing uploads, and not a
	// module-agnostic place: the upload belongs to the module that took it.
	mux.Handle("POST /reviews/uploads", auth(http.HandlerFunc(d.Trust.CreateUpload)))
	mux.Handle("POST /reviews/uploads/{id}/confirmation", auth(http.HandlerFunc(d.Trust.ConfirmUpload)))
	mux.Handle("POST /reports", auth(http.HandlerFunc(d.Trust.SubmitReport)))
	mux.Handle("GET /reports", auth(http.HandlerFunc(d.Trust.ListMyReports)))
	mux.Handle("GET /admin/reports", auth(http.HandlerFunc(d.Trust.AdminListReports)))
	mux.Handle("GET /admin/reports/{id}", auth(http.HandlerFunc(d.Trust.AdminGetReport)))
	mux.Handle("POST /admin/reports/{id}/claim", auth(http.HandlerFunc(d.Trust.AdminClaimReport)))
	mux.Handle("POST /admin/reports/{id}/resolution", auth(http.HandlerFunc(d.Trust.AdminResolveReport)))

	// Metrics wraps the mux so it can read the matched route.
	// Metrics is optional (nil in tests that don't wire observability).
	var routed http.Handler = mux
	if d.Metrics != nil {
		routed = d.Metrics.Middleware(mux)
	}

	// Routes are registered unprefixed and mounted under the versioned base path, so
	// the prefix lives only in openapi.BasePath. StripPrefix stays outside the
	// metrics middleware, which labels by the inner mux's matched pattern.
	root := http.NewServeMux()
	root.Handle(openapi.BasePath+"/", http.StripPrefix(openapi.BasePath, routed))
	// Provider callbacks. Mounted on the root rather than under the API base path,
	// because the URL a provider was given is not ours to version.
	if d.Webhooks != nil {
		root.Handle("/webhooks/", d.Webhooks)
	}

	return middleware.Logging(d.Log)(root)
}
