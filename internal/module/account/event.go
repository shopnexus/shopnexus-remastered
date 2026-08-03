package account

import (
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/shared/realtime"
)

// NotificationCreated is a feed row appearing. It goes to the account the row belongs
// to and to nobody else — unlike an order fact, a notification has exactly one
// interested party by construction.
var NotificationCreated = realtime.NewEvent[accountapi.Notification]("account.notification_created")
