package analyticmodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Event string

const (
	// High intent events.
	EventPurchase     Event = "purchase"
	EventAddToCart    Event = "add_to_cart"
	EventView         Event = "view"
	EventWriteReview  Event = "write_review"
	EventRatingHigh   Event = "rating_high"
	EventRatingMedium Event = "rating_medium"
	EventRatingLow    Event = "rating_low"

	// Discovery events.
	EventClickFromSearch     Event = "click_from_search"
	EventClickFromRecommend  Event = "click_from_recommendation"
	EventClickFromCategory   Event = "click_from_category"
	EventViewSimilarProducts Event = "view_similar_products"

	// Funnel events.
	EventProductImpression Event = "product_impression"
	EventCheckoutStarted   Event = "checkout_started"

	// Additional signals.
	EventAddToFavorites Event = "add_to_favorites"
	EventAskQuestion    Event = "ask_question"

	// Negative signals.
	EventRemoveFromCart  Event = "remove_from_cart"
	EventCancelOrder     Event = "cancel_order"
	EventRefundRequested Event = "refund_requested"
	EventReturnProduct   Event = "return_product"
	EventReportProduct   Event = "report_product"
	EventDislike         Event = "dislike"
	EventHideItem        Event = "hide_item"
	EventNotInterested   Event = "not_interested"
	EventViewBounce      Event = "view_bounce"
)

type Interaction struct {
	ID          int64              `json:"id"`
	AccountID   uuid.NullUUID      `json:"account_id"`
	EventType   Event              `json:"event_type"`
	RefType     InteractionRefType `json:"ref_type"`
	RefID       uuid.UUID          `json:"ref_id"`
	Metadata    json.RawMessage    `json:"metadata"`
	DateCreated time.Time          `json:"date_created"`
}
