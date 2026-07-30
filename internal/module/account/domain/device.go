package domain

import (
	"time"

	"shopnexus/internal/shared/validation"
)

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformWeb     Platform = "web"
)

// tokenSuffixLen is how much of a push token comes back to the client: enough to
// recognise its own install in a list, useless to anyone who intercepts it.
const tokenSuffixLen = 8

// Device is one install registered for push. The push token is the key that
// matters, not the row id: the platform reissues the same token when a different
// user signs in on that phone, so registering moves the row to the new account —
// otherwise the previous owner keeps receiving that phone's notifications.
type Device struct {
	ID         int64
	AccountID  int64
	Platform   Platform `validate:"required,oneof=ios android web"`
	PushToken  string   `validate:"required,min=1,max=4096"`
	LastSeenAt time.Time
	CreatedAt  time.Time
}

func NewDevice(accountID int64, platform Platform, pushToken string) (Device, error) {
	d := Device{AccountID: accountID, Platform: platform, PushToken: pushToken}
	if err := validation.Default().Struct(d); err != nil {
		return Device{}, validation.AsError(err)
	}
	return d, nil
}

// TokenSuffix is the only part of the token that is ever published. The whole
// token is a delivery credential.
func (d Device) TokenSuffix() string {
	if len(d.PushToken) <= tokenSuffixLen {
		return d.PushToken
	}
	return d.PushToken[len(d.PushToken)-tokenSuffixLen:]
}

func (d Device) Owns(accountID int64) bool { return d.AccountID == accountID }
