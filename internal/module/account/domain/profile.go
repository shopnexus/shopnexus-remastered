package domain

import (
	"regexp"
	"strings"
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type Gender string

const (
	GenderMale   Gender = "male"
	GenderFemale Gender = "female"
	GenderOther  Gender = "other"
)

var (
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	localeRe  = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
)

// earliestBirthDate matches the column's CHECK. A date before it is a typo, not a
// customer.
var earliestBirthDate = time.Date(1900, time.January, 2, 0, 0, 0, 0, time.UTC)

// Profile is the public face of an account and doubles as the shop page. It shares
// the account's primary key, so there is no separate profile id on the wire.
//
// Locale and timezone are not decoration: they decide the language a notification
// is written in and the local hour it is allowed to arrive at.
type Profile struct {
	ID          int64
	Name        string `validate:"required,min=1,max=100"`
	Description string `validate:"max=2000"`
	Gender      Gender `validate:"omitempty,oneof=male female other"`
	// DateOfBirth is a date, not an instant: it is stored as DATE and compared by
	// day, so the location on this value is never significant.
	DateOfBirth *time.Time
	// AvatarResourceID is a common.resource key; 0 means no avatar. Not unique —
	// several accounts may share one image.
	AvatarResourceID int64
	Country          string `validate:"required,len=2"`
	Locale           string `validate:"required,max=10"`
	Timezone         string `validate:"required,max=64"`
	CreatedAt        time.Time
}

func NewProfile(name, country, locale, timezone string) (Profile, error) {
	p := Profile{
		Name:     strings.TrimSpace(name),
		Country:  strings.ToUpper(strings.TrimSpace(country)),
		Locale:   strings.TrimSpace(locale),
		Timezone: strings.TrimSpace(timezone),
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (p Profile) Validate() error {
	if err := validation.Default().Struct(p); err != nil {
		return validation.AsError(err)
	}
	var fields []errx.Field
	if !countryRe.MatchString(p.Country) {
		fields = append(fields, errx.Field{Field: "country", Rule: "pattern", Message: "must be an ISO 3166-1 alpha-2 code"})
	}
	if !localeRe.MatchString(p.Locale) {
		fields = append(fields, errx.Field{Field: "locale", Rule: "pattern", Message: "must be a BCP 47 tag such as vi-VN"})
	}
	if p.DateOfBirth != nil {
		switch {
		case p.DateOfBirth.Before(earliestBirthDate):
			fields = append(fields, errx.Field{Field: "date_of_birth", Rule: "gt", Message: "must be after 1900-01-01"})
		case p.DateOfBirth.After(time.Now()):
			fields = append(fields, errx.Field{Field: "date_of_birth", Rule: "lte", Message: "must not be in the future"})
		}
	}
	if len(fields) > 0 {
		return errx.NewValidationError(summaryOf(fields), fields...)
	}
	return nil
}

// summaryOf keeps the one-line message in step with the field detail beside it.
func summaryOf(fields []errx.Field) string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Field)
	}
	if len(names) == 1 {
		return "invalid field: " + names[0]
	}
	return "invalid fields: " + strings.Join(names, ", ")
}
