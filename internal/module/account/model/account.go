package accountmodel

import (
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type Profile struct {
	ID          uuid.UUID `json:"id"`
	DateCreated time.Time `json:"date_created"`
	DateUpdated time.Time `json:"date_updated"`

	// Account base
	Status   Status      `json:"status"`
	Role     Role        `json:"role"`
	Phone    null.String `json:"phone"`
	Email    null.String `json:"email"`
	Username null.String `json:"username"`

	// Profile fields
	Gender           NullGender    `json:"gender"`
	Name             null.String   `json:"name"`
	DateOfBirth      null.Time     `json:"date_of_birth"`
	EmailVerified    bool          `json:"email_verified"`
	PhoneVerified    bool          `json:"phone_verified"`
	Country          string        `json:"country"`
	Currency         string        `json:"currency"`
	DefaultContactID uuid.NullUUID `json:"default_contact_id"`
	AvatarURL        null.String   `json:"avatar_url"`

	// Description
	Description null.String `json:"description"`
}
