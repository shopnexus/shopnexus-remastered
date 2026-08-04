package accountapi

import (
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// The roles Me.Role reports, as every other module has to compare them. Published here rather
// than left to each caller's own literal: five modules gate a moderator route on this value, and
// a typo in one of them is a gate that silently opens. The account module's domain holds the same
// three values — its own vocabulary, which a `domain` package cannot publish.
const (
	RoleUser      = "user"
	RoleModerator = "moderator"
	RoleAdmin     = "admin"
)

// The identity-document statuses a moderator's queue filters on, as the published `status`
// reports them.
const (
	IdentityStatusPending  = "pending"
	IdentityStatusVerified = "verified"
	IdentityStatusRejected = "rejected"
)

// The error codes another module branches on. A coded refusal crosses the boundary as a string, so
// the name lives here beside the method that returns it — catalog refuses a publish differently
// when the reason is "this seller has no pickup address" than when the account module is simply
// unreachable.
const CodeNoPickupContact = "no_pickup_contact"

// SupportUsername is the reserved username of the support desk's account. Reserved: registration
// refuses it, because an account able to sign in as the desk could read every ticket thread.
const SupportUsername = "support"

// --- authentication ---

// AuthResult is what every successful sign-in returns: the two tokens, the access
// token's lifetime, and the account, so a client needs no second round trip to
// render the signed-in state.
type AuthResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// ExpiresIn is the access token's lifetime in seconds.
	ExpiresIn int `json:"expires_in"`
	Account   Me  `json:"account"`
}

// OAuthLoginResult carries the status distinction the endpoint answers with: a
// federated sign-in either found an account (200) or created one (201), and a
// client wants to know which so it can send a new seller through onboarding.
type OAuthLoginResult struct {
	Auth    AuthResult
	Created bool
}

// --- accounts ---

// Profile is the display half of an account, and the shop front of a seller.
type Profile struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Gender      *string `json:"gender"`
	// DateOfBirth is a plain date (2006-01-02): the day is the fact, and an instant
	// would drag a timezone into it that nobody set.
	DateOfBirth *string             `json:"date_of_birth"`
	Avatar      *common.ResourceDTO `json:"avatar"`
	Country     string              `json:"country"`
	Locale      string              `json:"locale"`
	Timezone    string              `json:"timezone"`
	CreatedAt   time.Time           `json:"created_at"`
}

// Me is the caller's own view. Everything on it is private to them, which is why
// it is a different type from PublicAccount rather than the same one with fields
// blanked — a field that is sometimes secret gets published by accident.
type Me struct {
	ID            id.ID[id.Account] `json:"id"`
	Email         *string           `json:"email"`
	EmailVerified bool              `json:"email_verified"`
	// Phone here is an identifier — something to sign in and be reached by — and
	// carries no verified flag; that belongs to the phone on a contact.
	Phone    *string `json:"phone"`
	Username *string `json:"username"`
	Role     string  `json:"role"`
	Status   string  `json:"status"`
	// HasPassword is false on a provider-only account, which is what makes unlinking
	// the last provider refusable.
	HasPassword bool `json:"has_password"`
	// IdentityVerified is whether a live verified identity document exists. The payout
	// gate reads this.
	IdentityVerified bool      `json:"identity_verified"`
	Profile          Profile   `json:"profile"`
	CreatedAt        time.Time `json:"created_at"`
}

// PublicAccount is what anyone may see: the seller page. No email, no phone, no
// birth date, no addresses.
type PublicAccount struct {
	ID          id.ID[id.Account]   `json:"id"`
	Name        string              `json:"name"`
	Description *string             `json:"description"`
	Avatar      *common.ResourceDTO `json:"avatar"`
	// IdentityVerified is shown as a trust signal.
	IdentityVerified bool  `json:"identity_verified"`
	FollowerCount    int64 `json:"follower_count"`
	// CreatedAt is "member since".
	CreatedAt time.Time `json:"created_at"`
}

// AccountSummary is the compact form used in follower and following lists and in
// the identity review queue.
type AccountSummary struct {
	ID     id.ID[id.Account]   `json:"id"`
	Name   string              `json:"name"`
	Avatar *common.ResourceDTO `json:"avatar,omitempty"`
}

type OAuthIdentity struct {
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"created_at"`
}

// UploadSlot is where to PUT, what to confirm afterwards, and until when.
type UploadSlot struct {
	ResourceID id.ID[id.Resource] `json:"resource_id"`
	URL        string             `json:"url"`
	// Headers the client must send with the PUT, when the signature covers any.
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// --- saved addresses ---

type Contact struct {
	ID                id.ID[id.Contact] `json:"id"`
	FullName          string            `json:"full_name"`
	Phone             string            `json:"phone"`
	PhoneVerified     bool              `json:"phone_verified"`
	AddressType       string            `json:"address_type"`
	IsDefaultDelivery bool              `json:"is_default_delivery"`
	IsDefaultPickup   bool              `json:"is_default_pickup"`
	Country           string            `json:"country"`
	ProvinceCode      string            `json:"province_code"`
	ProvinceName      string            `json:"province_name"`
	// DistrictCode is null where the country has no district tier.
	DistrictCode  *string        `json:"district_code"`
	DistrictName  *string        `json:"district_name"`
	WardCode      string         `json:"ward_code"`
	WardName      string         `json:"ward_name"`
	PostalCode    *string        `json:"postal_code"`
	Address       string         `json:"address"`
	AddressDetail *string        `json:"address_detail"`
	Latitude      *float64       `json:"latitude"`
	Longitude     *float64       `json:"longitude"`
	ProviderCodes map[string]any `json:"provider_codes,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// --- push devices ---

type Device struct {
	ID       id.ID[id.Device] `json:"id"`
	Platform string           `json:"platform"`
	// PushTokenSuffix is the tail of the token, enough for a client to recognise its
	// own install. The whole token is a delivery credential and is never returned.
	PushTokenSuffix string    `json:"push_token_suffix"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// --- notifications ---

// Notification carries no id: the feed is a time-partitioned table read newest
// first, and POST /notifications/read takes a time bound, so a row never has to be
// named individually.
type Notification struct {
	Category string         `json:"category"`
	Title    string         `json:"title"`
	Payload  map[string]any `json:"payload"`
	ReadAt   *time.Time     `json:"read_at"`
	// CreatedAt identifies the row together with the feed order, and is what the
	// mark-read bound is given against.
	CreatedAt time.Time `json:"created_at"`
}

type UnreadCount struct {
	Unread int64 `json:"unread"`
}

type NotificationPreference struct {
	Category  string `json:"category"`
	Channel   string `json:"channel"`
	IsEnabled bool   `json:"is_enabled"`
	// IsDefault is true when no stored row exists and this is the domain default, so
	// a client can tell an explicit choice from an inherited one.
	IsDefault bool `json:"is_default"`
}

// --- payout identity verification ---

type IdentityDocument struct {
	ID       id.ID[id.IdentityDocument] `json:"id"`
	DocType  string                     `json:"doc_type"`
	Provider string                     `json:"provider"`
	Status   string                     `json:"status"`
	// RejectionReason and VerifiedAt each belong to exactly one status.
	RejectionReason *string    `json:"rejection_reason"`
	VerifiedAt      *time.Time `json:"verified_at"`
	// ExpiresAt is when the document itself runs out; a payout gate reads it as well
	// as the status.
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// IdentityVerificationTicket is a freshly opened case plus where the caller
// finishes it with the vendor.
type IdentityVerificationTicket struct {
	Document               IdentityDocument `json:"document"`
	VendorSessionURL       *string          `json:"vendor_session_url"`
	VendorSessionExpiresAt *time.Time       `json:"vendor_session_expires_at"`
}

// AdminIdentityDocument is a review-queue entry, which needs the subject beside the
// document.
type AdminIdentityDocument struct {
	Document IdentityDocument `json:"document"`
	Account  AccountSummary   `json:"account"`
}

// --- moderator and admin ---

// AdminAccount is the staff view: the identifiers and the suspension state that
// PublicAccount withholds.
type AdminAccount struct {
	ID            id.ID[id.Account] `json:"id"`
	Email         *string           `json:"email"`
	EmailVerified bool              `json:"email_verified"`
	Phone         *string           `json:"phone"`
	Username      *string           `json:"username"`
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	Status        string            `json:"status"`
	// SuspendedUntil null while suspended means the suspension is permanent.
	SuspendedUntil   *time.Time `json:"suspended_until"`
	SuspensionReason *string    `json:"suspension_reason"`
	IdentityVerified bool       `json:"identity_verified"`
	CreatedAt        time.Time  `json:"created_at"`
}
