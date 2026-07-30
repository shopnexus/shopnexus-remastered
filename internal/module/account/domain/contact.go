package domain

import (
	"strings"
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type AddressType string

const (
	AddressTypeHome AddressType = "home"
	AddressTypeWork AddressType = "work"
)

// Contact is a saved address. The administrative codes are what a carrier is
// called with, so they are the routing source of truth and the names beside them
// are display snapshots — a territorial rename must not rewrite a saved address.
//
// The phone here is the one a courier actually dials, which is why this one
// carries a verified flag and the account's sign-in phone does not.
type Contact struct {
	ID                int64
	AccountID         int64
	FullName          string `validate:"required,min=1,max=100"`
	Phone             string `validate:"required,e164"`
	PhoneVerified     bool
	AddressType       AddressType `validate:"required,oneof=home work"`
	IsDefaultDelivery bool
	IsDefaultPickup   bool

	Country      string `validate:"required,len=2"`
	ProvinceCode string `validate:"required,max=20"`
	ProvinceName string `validate:"required,max=100"`
	// DistrictCode and DistrictName are set together or not at all: Vietnam dropped
	// the district tier in 2025 and goes province to ward, other countries still
	// have one. The column has the same CHECK.
	DistrictCode  string `validate:"max=20"`
	DistrictName  string `validate:"max=100"`
	WardCode      string `validate:"required,max=20"`
	WardName      string `validate:"required,max=100"`
	PostalCode    string `validate:"max=20"`
	Address       string `validate:"required,min=1,max=255"`
	AddressDetail string `validate:"max=255"`
	// Latitude and Longitude are advisory — geocoding may fail and the address still
	// has to be saveable — and travel together.
	Latitude  *float64
	Longitude *float64
	// ProviderCodes holds per-carrier territory ids, e.g.
	// {"ghn": {"district_id": 1442}}. Carriers number territories their own way.
	ProviderCodes map[string]any
	CreatedAt     time.Time
}

func (c Contact) Validate() error {
	if err := validation.Default().Struct(c); err != nil {
		return validation.AsError(err)
	}
	var fields []errx.Field
	if !countryRe.MatchString(c.Country) {
		fields = append(fields, errx.Field{Field: "country", Rule: "pattern", Message: "must be an ISO 3166-1 alpha-2 code"})
	}
	if (c.DistrictCode == "") != (c.DistrictName == "") {
		fields = append(fields, errx.Field{Field: "district_code", Rule: "together", Message: "send both district fields or neither"})
	}
	if (c.Latitude == nil) != (c.Longitude == nil) {
		fields = append(fields, errx.Field{Field: "latitude", Rule: "together", Message: "send both coordinates or neither"})
	}
	if len(fields) > 0 {
		return errx.NewValidationError(summaryOf(fields), fields...)
	}
	return nil
}

// SetPhone changes the number a carrier will call and clears the verified flag with
// it — a flag on a number nobody checked is worse than no flag.
func (c *Contact) SetPhone(v string) {
	next := strings.TrimSpace(v)
	if next == c.Phone {
		return
	}
	c.Phone = next
	c.PhoneVerified = false
}

// Owns reports whether the contact belongs to the account, which is the check
// behind every 403 on this resource.
func (c Contact) Owns(accountID int64) bool { return c.AccountID == accountID }
