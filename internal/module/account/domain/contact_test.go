package domain_test

import (
	"testing"

	"shopnexus/internal/module/account/domain"
)

func contact() domain.Contact {
	lat, lng := 10.77, 106.7
	return domain.Contact{
		AccountID:    1,
		FullName:     "Alice",
		Phone:        "+84901234567",
		AddressType:  domain.AddressTypeHome,
		Country:      "VN",
		ProvinceCode: "79",
		ProvinceName: "Ho Chi Minh",
		WardCode:     "27154",
		WardName:     "Ben Nghe",
		Address:      "1 Le Loi",
		Latitude:     &lat,
		Longitude:    &lng,
	}
}

func TestContactValidate(t *testing.T) {
	if err := contact().Validate(); err != nil {
		t.Fatalf("a plain contact is invalid: %v", err)
	}
	for _, tc := range []struct {
		name  string
		build func(*domain.Contact)
	}{
		// These two travel together in the column CHECK as well: Vietnam dropped the
		// district tier, other countries still have one.
		{name: "district code without a name", build: func(c *domain.Contact) { code := "760"; c.DistrictCode = &code }},
		{name: "district name without a code", build: func(c *domain.Contact) { name := "District 1"; c.DistrictName = &name }},
		{name: "latitude without a longitude", build: func(c *domain.Contact) { c.Longitude = nil }},
		{name: "longitude without a latitude", build: func(c *domain.Contact) { c.Latitude = nil }},
		{name: "no full name", build: func(c *domain.Contact) { c.FullName = "" }},
		{name: "phone not E.164", build: func(c *domain.Contact) { c.Phone = "0901234567" }},
		{name: "latitude out of range", build: func(c *domain.Contact) { lat := 999.0; c.Latitude = &lat }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := contact()
			tc.build(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

// A verified flag on a number nobody checked is worse than no flag, so changing the number
// clears it — and re-sending the same one does not.
func TestContactSetPhone_ClearsVerified(t *testing.T) {
	c := contact()
	c.PhoneVerified = true
	c.SetPhone("+84901234567")
	if !c.PhoneVerified {
		t.Fatal("a no-op change cleared the flag")
	}
	c.SetPhone("+84987654321")
	if c.PhoneVerified {
		t.Fatal("the flag survived a new number")
	}
}
