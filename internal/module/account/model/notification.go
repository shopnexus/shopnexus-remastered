package accountmodel

// NotificationType defines the category of a notification.
type NotificationType string

const (
	// Order flow — buyer notifications.
	NotiOrderCancelled     NotificationType = "order_cancelled"
	NotiItemsConfirmed     NotificationType = "items_confirmed"
	NotiItemsRejected      NotificationType = "items_rejected"
	NotiPaymentSuccess     NotificationType = "payment_success"
	NotiPaymentFailed      NotificationType = "payment_failed"
	NotiRefundApproved     NotificationType = "refund_approved"
	NotiTransportDelivered NotificationType = "transport_delivered"
	NotiTransportFailed    NotificationType = "transport_failed"
	NotiTransportCancelled NotificationType = "transport_cancelled"

	// Order flow — seller notifications.
	NotiNewPendingItems          NotificationType = "new_pending_items"
	NotiPendingItemCancelled     NotificationType = "pending_item_cancelled"
	NotiRefundRequested          NotificationType = "refund_requested"
	NotiDisputeOpened            NotificationType = "dispute_opened"
	NotiSellerTransportFailed    NotificationType = "seller_transport_failed"
	NotiSellerTransportCancelled NotificationType = "seller_transport_cancelled"
	NotiPayoutReleased           NotificationType = "payout_released"
	NotiPayoutCancelled          NotificationType = "payout_cancelled"

	// Catalog — seller notifications.
	NotiNewReview NotificationType = "new_review"

	// Account.
	NotiWelcome              NotificationType = "welcome"
	NotiPaymentMethodAdded   NotificationType = "payment_method_added"
	NotiPaymentMethodDeleted NotificationType = "payment_method_deleted"
)

// NotificationChannel defines the delivery mechanism.
type NotificationChannel string

const (
	ChannelInApp NotificationChannel = "in_app"
)
