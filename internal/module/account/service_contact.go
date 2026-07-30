package account

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/id"
)

func (s *Service) ListContacts(ctx context.Context, req accountapi.ListContactsRequest) ([]accountapi.Contact, error) {
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

func (s *Service) CreateContact(ctx context.Context, req accountapi.CreateContactRequest) (accountapi.Contact, error) {
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
		DistrictCode:      req.DistrictCode,
		DistrictName:      req.DistrictName,
		WardCode:          req.WardCode,
		WardName:          req.WardName,
		PostalCode:        req.PostalCode,
		Address:           req.Address,
		AddressDetail:     req.AddressDetail,
		Latitude:          req.Latitude,
		Longitude:         req.Longitude,
	}
	if err := c.Validate(); err != nil {
		return accountapi.Contact{}, err
	}
	if err := s.repo.InsertContact(ctx, &c); err != nil {
		return accountapi.Contact{}, fmt.Errorf("insert contact: %w", err)
	}
	return toAPIContact(c), nil
}

func (s *Service) UpdateContact(ctx context.Context, req accountapi.UpdateContactRequest) (accountapi.Contact, error) {
	c, err := s.ownedContact(ctx, req.ActorID, req.ID)
	if err != nil {
		return accountapi.Contact{}, err
	}
	if v, ok := req.FullName.Get(); ok {
		c.FullName = v
	}
	if v, ok := req.Phone.Get(); ok {
		// SetPhone, not an assignment: changing the number clears the verified flag with it.
		c.SetPhone(v)
	}
	if v, ok := req.AddressType.Get(); ok {
		c.AddressType = domain.AddressType(v)
	}
	if v, ok := req.IsDefaultDelivery.Get(); ok {
		c.IsDefaultDelivery = v
	}
	if v, ok := req.IsDefaultPickup.Get(); ok {
		c.IsDefaultPickup = v
	}
	if v, ok := req.ProvinceCode.Get(); ok {
		c.ProvinceCode = v
	}
	if v, ok := req.ProvinceName.Get(); ok {
		c.ProvinceName = v
	}
	if req.DistrictCode.Present() {
		c.DistrictCode = valueOrEmpty(req.DistrictCode.Ptr())
	}
	if req.DistrictName.Present() {
		c.DistrictName = valueOrEmpty(req.DistrictName.Ptr())
	}
	if v, ok := req.WardCode.Get(); ok {
		c.WardCode = v
	}
	if v, ok := req.WardName.Get(); ok {
		c.WardName = v
	}
	if req.PostalCode.Present() {
		c.PostalCode = valueOrEmpty(req.PostalCode.Ptr())
	}
	if v, ok := req.Address.Get(); ok {
		c.Address = v
	}
	if req.AddressDetail.Present() {
		c.AddressDetail = valueOrEmpty(req.AddressDetail.Ptr())
	}
	if req.Latitude.Present() {
		c.Latitude = req.Latitude.Ptr()
	}
	if req.Longitude.Present() {
		c.Longitude = req.Longitude.Ptr()
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
	c, err := s.ownedContact(ctx, req.ActorID, req.ID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteContact(ctx, c.ID); err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}

// RequestContactPhoneVerification sends a code to the number a courier will actually
// dial. Re-sending replaces the outstanding code rather than issuing a second valid one,
// which is what the single key per contact gives us for free.
func (s *Service) RequestContactPhoneVerification(ctx context.Context, req accountapi.RequestContactPhoneVerificationRequest) error {
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

// ownedContact is the one place a 404 and a 403 on this resource are told apart: a
// contact that does not exist is not found, and one belonging to somebody else is
// forbidden.
func (s *Service) ownedContact(ctx context.Context, actorID id.ID[id.Account], contactID id.ID[id.Contact]) (domain.Contact, error) {
	c, err := s.repo.FindContact(ctx, contactID.Int64())
	if err != nil {
		return domain.Contact{}, fmt.Errorf("find contact: %w", err)
	}
	if !c.Owns(actorID.Int64()) {
		return domain.Contact{}, domain.ErrForbidden
	}
	return c, nil
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
		DistrictCode:      optional(c.DistrictCode),
		DistrictName:      optional(c.DistrictName),
		WardCode:          c.WardCode,
		WardName:          c.WardName,
		PostalCode:        optional(c.PostalCode),
		Address:           c.Address,
		AddressDetail:     optional(c.AddressDetail),
		Latitude:          c.Latitude,
		Longitude:         c.Longitude,
		ProviderCodes:     c.ProviderCodes,
		CreatedAt:         c.CreatedAt,
	}
}
