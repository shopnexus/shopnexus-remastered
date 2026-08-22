package account

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/areas"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

// ListAdministrativeAreas serves one level of the division tree from the list vendored in this
// module. No repository and no cache: the file is in the binary, so the answer is a slice that was
// built once at first use.
func (s *Service) ListAdministrativeAreas(ctx context.Context, req accountapi.ListAdministrativeAreasRequest) ([]accountapi.AdministrativeArea, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	found, ok, err := areas.Children(req.Parent)
	if err != nil {
		return nil, fmt.Errorf("read administrative areas: %w", err)
	}
	if !ok {
		return nil, domain.ErrAreaNotFound
	}
	out := make([]accountapi.AdministrativeArea, 0, len(found))
	for _, a := range found {
		out = append(out, accountapi.AdministrativeArea{Code: a.Code, Name: a.Name, Kind: a.Kind})
	}
	return out, nil
}

func (s *Service) ListContacts(ctx context.Context, req accountapi.ListContactsRequest) ([]accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListContacts(ctx, req.ActorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	out := make([]accountapi.Contact, 0, len(rows))
	for _, c := range rows {
		out = append(out, toAPIContact(c))
	}
	return out, nil
}

// GetContact reads one of the caller's own. Somebody else's is not found rather than
// forbidden — it is not theirs to know about.
func (s *Service) GetContact(ctx context.Context, req accountapi.GetContactRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	c, err := s.repo.FindContact(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return accountapi.Contact{}, fmt.Errorf("find contact: %w", err)
	}
	return toAPIContact(c), nil
}

// GetPickupContact answers a seller's collection point. No actor: the order module calls it
// when the money lands and the seller is not there, and only the pickup default is exposed
// — the rest of somebody's addresses are not another module's business.
func (s *Service) GetPickupContact(ctx context.Context, req accountapi.GetPickupContactRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	rows, err := s.repo.ListContacts(ctx, req.AccountID.Int64())
	if err != nil {
		return accountapi.Contact{}, fmt.Errorf("list contacts: %w", err)
	}
	for _, c := range rows {
		if c.IsDefaultPickup {
			return toAPIContact(c), nil
		}
	}
	return accountapi.Contact{}, domain.ErrNoPickupContact
}

// GetDeliveryContact is the buyer's side of GetPickupContact: the one address their parcels go to
// unless they choose another, which is what a quote uses when no contact was named.
func (s *Service) GetDeliveryContact(ctx context.Context, req accountapi.GetDeliveryContactRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	rows, err := s.repo.ListContacts(ctx, req.ActorID.Int64())
	if err != nil {
		return accountapi.Contact{}, fmt.Errorf("list contacts: %w", err)
	}
	for _, c := range rows {
		if c.IsDefaultDelivery {
			return toAPIContact(c), nil
		}
	}
	return accountapi.Contact{}, domain.ErrNoDeliveryContact
}

// canonicalizeArea makes the names on the wire advisory: a client sends codes, and what is stored
// is what those codes mean here. Denormalized is not client-owned — taking the name from the
// request let one code carry three names ("Ha Noi", "Hà Nội", "Thành phố Hà Nội" all under 01).
func canonicalizeArea(c *domain.Contact) error {
	provinceName, wardName, err := areas.Resolve(c.ProvinceCode, c.WardCode)
	if err != nil {
		if errors.Is(err, areas.ErrUnknownArea) {
			return errx.NewValidationError("invalid field: province_code", errx.Field{
				Field: "province_code", Rule: "oneof",
				Message: "no such province and ward: send a code pair from /administrative-areas",
			})
		}
		return fmt.Errorf("resolve administrative area: %w", err)
	}
	c.ProvinceName, c.WardName = provinceName, wardName
	return nil
}

func (s *Service) CreateContact(ctx context.Context, req accountapi.CreateContactRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	c := domain.Contact{
		AccountID:         req.ActorID.Int64(),
		FullName:          req.FullName,
		Phone:             req.Phone,
		AddressType:       domain.AddressType(req.AddressType),
		IsDefaultDelivery: req.IsDefaultDelivery,
		IsDefaultPickup:   req.IsDefaultPickup,
		Country:           req.Country,
		ProvinceCode:      req.ProvinceCode,
		ProvinceName:      req.ProvinceName,
		WardCode:          req.WardCode,
		WardName:          req.WardName,
		Address:           req.Address,
		Latitude:          req.Latitude,
		Longitude:         req.Longitude,
	}
	// A nullable column left empty on a create is not set at all, so nil stays the one way
	// to spell absent. The district pair is filled in field by field on purpose: half a
	// district has to reach Validate as one nil and one value so it can be refused.
	if req.DistrictCode != "" {
		c.DistrictCode = &req.DistrictCode
	}
	if req.DistrictName != "" {
		c.DistrictName = &req.DistrictName
	}
	if req.PostalCode != "" {
		c.PostalCode = &req.PostalCode
	}
	if req.AddressDetail != "" {
		c.AddressDetail = &req.AddressDetail
	}
	if err := canonicalizeArea(&c); err != nil {
		return accountapi.Contact{}, err
	}
	if err := c.Validate(); err != nil {
		return accountapi.Contact{}, err
	}
	if err := s.repo.InsertContact(ctx, &c); err != nil {
		return accountapi.Contact{}, fmt.Errorf("insert contact: %w", err)
	}
	return toAPIContact(c), nil
}

// UpdateContact reads the row, applies the patch, validates the result: the rules that
// matter are about the whole address — both district fields or neither, both coordinates
// or neither — so they cannot be checked one field at a time.
func (s *Service) UpdateContact(ctx context.Context, req accountapi.UpdateContactRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	c, err := s.ownedContact(ctx, req.ActorID, req.ID)
	if err != nil {
		return accountapi.Contact{}, err
	}
	// A required column: absent leaves it, a value replaces it.
	if req.FullName != nil {
		c.FullName = *req.FullName
	}
	if req.IsDefaultDelivery != nil {
		c.IsDefaultDelivery = *req.IsDefaultDelivery
	}
	if req.IsDefaultPickup != nil {
		c.IsDefaultPickup = *req.IsDefaultPickup
	}
	if req.Country != nil {
		c.Country = *req.Country
	}
	if req.ProvinceCode != nil {
		c.ProvinceCode = *req.ProvinceCode
	}
	if req.ProvinceName != nil {
		c.ProvinceName = *req.ProvinceName
	}
	if req.WardCode != nil {
		c.WardCode = *req.WardCode
	}
	if req.WardName != nil {
		c.WardName = *req.WardName
	}
	if req.Address != nil {
		c.Address = *req.Address
	}
	// A nullable one also takes a clear. The district pair and the coordinate travel
	// together, so they clear together.
	if req.ClearDistrict {
		c.DistrictCode, c.DistrictName = nil, nil
	} else {
		if req.DistrictCode != nil {
			c.DistrictCode = req.DistrictCode
		}
		if req.DistrictName != nil {
			c.DistrictName = req.DistrictName
		}
	}
	switch {
	case req.ClearPostalCode:
		c.PostalCode = nil
	case req.PostalCode != nil:
		c.PostalCode = req.PostalCode
	}
	switch {
	case req.ClearAddressDetail:
		c.AddressDetail = nil
	case req.AddressDetail != nil:
		c.AddressDetail = req.AddressDetail
	}
	if req.ClearLocation {
		c.Latitude, c.Longitude = nil, nil
	} else {
		if req.Latitude != nil {
			c.Latitude = req.Latitude
		}
		if req.Longitude != nil {
			c.Longitude = req.Longitude
		}
	}
	// Not assignments: a new number clears the verified flag, and the address type is
	// narrower than the string on the wire.
	if req.Phone != nil {
		c.SetPhone(*req.Phone)
	}
	if req.AddressType != nil {
		c.AddressType = domain.AddressType(*req.AddressType)
	}
	// After the patch, not per field: one request may move both, and the pair resolves only once.
	if err := canonicalizeArea(&c); err != nil {
		return accountapi.Contact{}, err
	}
	if err := c.Validate(); err != nil {
		return accountapi.Contact{}, err
	}
	if err := s.repo.UpdateContact(ctx, c); err != nil {
		return accountapi.Contact{}, fmt.Errorf("update contact: %w", err)
	}
	return toAPIContact(c), nil
}

func (s *Service) DeleteContact(ctx context.Context, req accountapi.DeleteContactRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	if err := s.repo.DeleteContact(ctx, req.ActorID.Int64(), req.ID.Int64()); err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}

// RequestContactPhoneVerification sends a code to the number a courier will actually
// dial. Re-sending replaces the outstanding code rather than issuing a second valid one,
// which is what the single key per contact gives us for free.
func (s *Service) RequestContactPhoneVerification(ctx context.Context, req accountapi.RequestContactPhoneVerificationRequest) error {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return err
	}
	c, err := s.ownedContact(ctx, req.ActorID, req.ID)
	if err != nil {
		return err
	}
	if c.PhoneVerified {
		return domain.ErrContactPhoneAlreadyVerified
	}
	if err := s.throttle(ctx, "contact-phone", c.Phone); err != nil {
		return err
	}
	code, err := mintCode()
	if err != nil {
		return err
	}
	if err := s.cache.Set(ctx, phoneCodeKey(c.ID), code, phoneCodeTTL); err != nil {
		return fmt.Errorf("store phone code: %w", err)
	}
	s.send(ctx, notify.Message{
		Kind:   notify.KindPhoneCode,
		Phone:  c.Phone,
		Token:  code,
		Locale: s.localeOf(ctx, c.AccountID),
	})
	return nil
}

func (s *Service) VerifyContactPhone(ctx context.Context, req accountapi.VerifyContactPhoneRequest) (accountapi.Contact, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return accountapi.Contact{}, err
	}
	c, err := s.ownedContact(ctx, req.ActorID, req.ID)
	if err != nil {
		return accountapi.Contact{}, err
	}
	var want string
	if err := s.cache.Get(ctx, phoneCodeKey(c.ID), &want); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return accountapi.Contact{}, domain.ErrInvalidPhoneCode
		}
		return accountapi.Contact{}, fmt.Errorf("read phone code: %w", err)
	}
	// Constant-time, because the comparison is against a secret the caller is guessing.
	if subtle.ConstantTimeCompare([]byte(want), []byte(req.Code)) != 1 {
		return accountapi.Contact{}, domain.ErrInvalidPhoneCode
	}
	if err := s.cache.Delete(ctx, phoneCodeKey(c.ID)); err != nil {
		return accountapi.Contact{}, fmt.Errorf("delete used phone code: %w", err)
	}
	c.PhoneVerified = true
	if err := s.repo.UpdateContact(ctx, c); err != nil {
		return accountapi.Contact{}, fmt.Errorf("update contact: %w", err)
	}
	return toAPIContact(c), nil
}

// ownedContact reads one of the caller's saved addresses. The owner is part of the lookup,
// so somebody else's contact is a 404 rather than a 403 — the resource is not theirs to
// know about.
func (s *Service) ownedContact(ctx context.Context, actorID id.ID[id.Account], contactID id.ID[id.Contact]) (domain.Contact, error) {
	return s.repo.FindContact(ctx, actorID.Int64(), contactID.Int64())
}

func phoneCodeKey(contactID int64) string {
	return phoneCodePrefix + strconv.FormatInt(contactID, 10)
}

func toAPIContact(c domain.Contact) accountapi.Contact {
	return accountapi.Contact{
		ID:                id.Of[id.Contact](c.ID),
		FullName:          c.FullName,
		Phone:             c.Phone,
		PhoneVerified:     c.PhoneVerified,
		AddressType:       string(c.AddressType),
		IsDefaultDelivery: c.IsDefaultDelivery,
		IsDefaultPickup:   c.IsDefaultPickup,
		Country:           c.Country,
		ProvinceCode:      c.ProvinceCode,
		ProvinceName:      c.ProvinceName,
		DistrictCode:      c.DistrictCode,
		DistrictName:      c.DistrictName,
		WardCode:          c.WardCode,
		WardName:          c.WardName,
		PostalCode:        c.PostalCode,
		Address:           c.Address,
		AddressDetail:     c.AddressDetail,
		Latitude:          c.Latitude,
		Longitude:         c.Longitude,
		ProviderCodes:     c.ProviderCodes,
		CreatedAt:         c.CreatedAt,
	}
}
