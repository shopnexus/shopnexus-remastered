package account_test

import (
	"context"
	"testing"
	"time"

	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
)

func contactRequest() accountapi.CreateContactRequest {
	return accountapi.CreateContactRequest{
		FullName:          "Nguyễn A",
		Phone:             "+84901234567",
		AddressType:       "home",
		IsDefaultDelivery: true,
		Country:           "VN",
		ProvinceCode:      "79",
		ProvinceName:      "TP HCM",
		WardCode:          "26734",
		WardName:          "Bến Nghé",
		Address:           "1 Lê Lợi",
	}
}

// --- saved addresses ---

func TestCreateContact_StoresAndLists(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())

	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if created.PhoneVerified {
		t.Error("a new address must not start out phone-verified")
	}
	// The district tier is absent in Vietnam, and null is how that is said.
	if created.DistrictCode != nil {
		t.Errorf("district_code = %v, want null", *created.DistrictCode)
	}

	list, err := h.svc.ListContacts(ctx, accountapi.ListContactsRequest{ActorID: owner.Account.ID})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
}

// Send both district fields or neither: the column has the same CHECK, and half an address
// is not routable.
func TestCreateContact_HalfADistrictRejected(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	req.DistrictCode = "760"

	if got := status(t, mustErr(h.svc.CreateContact(context.Background(), req))); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// The same rule on a PATCH, which is a separate code path: sending one district field
// leaves the other as it was, so the pair has to reach Validate half-set and be refused.
func TestUpdateContact_HalfADistrictRejected(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	districtCode := "760"
	_, err = h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID, DistrictCode: &districtCode,
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// Someone else's address is not found rather than forbidden: a contact is reached through
// its account's aggregate, so one that is not in the caller's set does not exist to them —
// and 404 leaks less than 403, which confirms the id is real.
func TestUpdateContact_OtherAccountNotFound(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	intruderReq := registerRequest()
	intruderReq.Email = "bob@example.com"
	intruder := h.register(t, intruderReq)

	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	hijacked := "Hijack"
	_, err = h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: intruder.Account.ID, ID: created.ID, FullName: &hijacked,
	})
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// Changing the number clears the verified flag: a flag on a number nobody checked is worse
// than no flag.
func TestUpdateContact_NewPhoneIsUnverified(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	// Verify it the way the API does, through the code that was sent.
	if err := h.svc.RequestContactPhoneVerification(ctx, accountapi.RequestContactPhoneVerificationRequest{
		ActorID: owner.Account.ID, ID: created.ID,
	}); err != nil {
		t.Fatalf("RequestContactPhoneVerification: %v", err)
	}
	verified, err := h.svc.VerifyContactPhone(ctx, accountapi.VerifyContactPhoneRequest{
		ActorID: owner.Account.ID, ID: created.ID, Code: h.phoneCode(t, created.ID.Int64()),
	})
	if err != nil {
		t.Fatalf("VerifyContactPhone: %v", err)
	}
	if !verified.PhoneVerified {
		t.Fatal("phone_verified = false after a correct code")
	}

	newPhone := "+84909999999"
	updated, err := h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID, Phone: &newPhone,
	})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if updated.PhoneVerified {
		t.Fatal("phone_verified survived a number change")
	}
}

// A required field has no clear flag, so the only way to empty it is to send an empty
// value — and that is refused rather than silently dropped: answering 200 to a request
// that was not carried out leaves the client with no way to find out.
func TestUpdateContact_EmptyingARequiredFieldRejected(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	empty := ""
	_, err = h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID, FullName: &empty,
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
	// And the stored contact is untouched, since the patch never reached the repository.
	list, err := h.svc.ListContacts(ctx, accountapi.ListContactsRequest{ActorID: owner.Account.ID})
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if list[0].FullName != req.FullName {
		t.Errorf("full_name = %q, want it unchanged as %q", list[0].FullName, req.FullName)
	}
}

// A PATCH goes through the same coordinate range as a POST. The rule lives on the entity
// precisely so the two paths cannot drift: a create that refuses latitude 999 and an update
// that accepts it puts a courier in the wrong hemisphere.
func TestUpdateContact_CoordinateRangeHoldsOnPatch(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	badLat, lng := 999.0, 106.6
	_, err = h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID,
		Latitude: &badLat, Longitude: &lng,
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}

	// And 0,0 is a real place, so it must still be accepted — the range check must not be
	// a disguised "is this the zero value" test.
	zeroLat, zeroLng := 0.0, 0.0
	if _, err := h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID,
		Latitude: &zeroLat, Longitude: &zeroLng,
	}); err != nil {
		t.Fatalf("UpdateContact with 0,0: %v", err)
	}
}

// The genuinely optional neighbours of that field do clear, which is the other half of the
// same rule — null means "remove this", and only Validate decides whether that is allowed.
func TestUpdateContact_ClearingAnOptionalFieldWorks(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	req.PostalCode = "70000"
	req.AddressDetail = "Apartment 4B"
	created, err := h.svc.CreateContact(ctx, req)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	updated, err := h.svc.UpdateContact(ctx, accountapi.UpdateContactRequest{
		ActorID: owner.Account.ID, ID: created.ID,
		ClearPostalCode:    true,
		ClearAddressDetail: true,
	})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if updated.PostalCode != nil || updated.AddressDetail != nil {
		t.Fatalf("postal_code = %v, address_detail = %v; want both null", updated.PostalCode, updated.AddressDetail)
	}
}

func TestVerifyContactPhone_WrongCodeRefused(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	req := contactRequest()
	req.ActorID = owner.Account.ID
	created, _ := h.svc.CreateContact(ctx, req)

	if err := h.svc.RequestContactPhoneVerification(ctx, accountapi.RequestContactPhoneVerificationRequest{
		ActorID: owner.Account.ID, ID: created.ID,
	}); err != nil {
		t.Fatalf("RequestContactPhoneVerification: %v", err)
	}
	_, err := h.svc.VerifyContactPhone(ctx, accountapi.VerifyContactPhoneRequest{
		ActorID: owner.Account.ID, ID: created.ID, Code: "000000",
	})
	if got := status(t, err); got != 401 {
		t.Fatalf("status = %d, want 401", got)
	}
}

// Setting a default clears the previous one, because the two are one choice.
func TestCreateContact_SecondDefaultMovesTheFlag(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())

	first := contactRequest()
	first.ActorID = owner.Account.ID
	a, err := h.svc.CreateContact(ctx, first)
	if err != nil {
		t.Fatalf("first CreateContact: %v", err)
	}
	second := contactRequest()
	second.ActorID = owner.Account.ID
	second.Address = "2 Lê Lợi"
	if _, err := h.svc.CreateContact(ctx, second); err != nil {
		t.Fatalf("second CreateContact: %v", err)
	}

	list, err := h.svc.ListContacts(ctx, accountapi.ListContactsRequest{ActorID: owner.Account.ID})
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	for _, c := range list {
		if c.ID == a.ID && c.IsDefaultDelivery {
			t.Fatal("both addresses claim the delivery default")
		}
	}
}

// --- push devices ---

// The token identifies an install, so the same phone signing in as someone else moves the
// row: otherwise the previous owner keeps getting that phone's notifications.
func TestRegisterDevice_MovesBetweenAccounts(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	first := h.register(t, registerRequest())
	secondReq := registerRequest()
	secondReq.Email = "bob@example.com"
	second := h.register(t, secondReq)

	a, err := h.svc.RegisterDevice(ctx, accountapi.RegisterDeviceRequest{
		ActorID: first.Account.ID, Platform: "ios", PushToken: "install-token",
	})
	if err != nil {
		t.Fatalf("first RegisterDevice: %v", err)
	}
	b, err := h.svc.RegisterDevice(ctx, accountapi.RegisterDeviceRequest{
		ActorID: second.Account.ID, Platform: "ios", PushToken: "install-token",
	})
	if err != nil {
		t.Fatalf("second RegisterDevice: %v", err)
	}
	if b.ID != a.ID {
		t.Errorf("device id = %v, want the same row %v", b.ID, a.ID)
	}
	if devices, err := h.svc.ListDevices(ctx, accountapi.ListDevicesRequest{ActorID: first.Account.ID}); err != nil || len(devices) != 0 {
		t.Errorf("the previous owner still has the device: %+v %v", devices, err)
	}
}

// The whole token is a delivery credential, so only its tail is ever published.
func TestRegisterDevice_PublishesOnlyTheTokenSuffix(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())

	d, err := h.svc.RegisterDevice(context.Background(), accountapi.RegisterDeviceRequest{
		ActorID: owner.Account.ID, Platform: "android", PushToken: "abcdefghijklmnop",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	if d.PushTokenSuffix == "abcdefghijklmnop" || len(d.PushTokenSuffix) != 8 {
		t.Fatalf("push_token_suffix = %q, want the tail only", d.PushTokenSuffix)
	}
}

func TestDeleteDevice_OtherAccountForbidden(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	intruderReq := registerRequest()
	intruderReq.Email = "bob@example.com"
	intruder := h.register(t, intruderReq)

	d, err := h.svc.RegisterDevice(ctx, accountapi.RegisterDeviceRequest{
		ActorID: owner.Account.ID, Platform: "web", PushToken: "t",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	err = h.svc.DeleteDevice(ctx, accountapi.DeleteDeviceRequest{ActorID: intruder.Account.ID, ID: d.ID})
	if got := status(t, err); got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

// --- notifications ---

// The feed is read newest first, one page at a time, and the cursor is what continues it.
func TestListNotifications_CursorWalksTheFeed(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	h.seedNotifications(owner.Account.ID.Int64(), 3)

	first, err := h.svc.ListNotifications(ctx, accountapi.ListNotificationsRequest{
		ActorID: owner.Account.ID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(first.Data) != 2 || first.NextCursor == nil {
		t.Fatalf("page = %+v, want two rows and a cursor", first)
	}
	// Newest first, so the last row of the first page is newer than the next page's.
	second, err := h.svc.ListNotifications(ctx, accountapi.ListNotificationsRequest{
		ActorID: owner.Account.ID, Limit: 2, Cursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListNotifications (page 2): %v", err)
	}
	if len(second.Data) != 1 {
		t.Fatalf("page 2 = %+v, want the last row", second.Data)
	}
	// The last page says so with a value rather than a missing key.
	if second.NextCursor != nil {
		t.Errorf("next_cursor = %v, want null on the last page", *second.NextCursor)
	}
	if !first.Data[0].CreatedAt.After(second.Data[0].CreatedAt) {
		t.Error("the feed is not newest-first")
	}
}

func TestListNotifications_GarbageCursorRejected(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())

	_, err := h.svc.ListNotifications(context.Background(), accountapi.ListNotificationsRequest{
		ActorID: owner.Account.ID, Limit: 20, Cursor: "not-a-cursor",
	})
	if got := status(t, err); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

// The bound covers "mark what I just scrolled past", and the answer carries the badge that
// follows so the client does not need a second call.
func TestMarkNotificationsRead_BoundAndBadge(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())
	created := h.seedNotifications(owner.Account.ID.Int64(), 3)

	unread, err := h.svc.GetUnreadCount(ctx, accountapi.GetUnreadCountRequest{ActorID: owner.Account.ID})
	if err != nil || unread.Unread != 3 {
		t.Fatalf("unread = %+v, err = %v", unread, err)
	}

	// created is oldest-first; bound at the middle row leaves the newest one unread.
	bound := created[1]
	after, err := h.svc.MarkNotificationsRead(ctx, accountapi.MarkNotificationsReadRequest{
		ActorID: owner.Account.ID, Before: &bound,
	})
	if err != nil {
		t.Fatalf("MarkNotificationsRead: %v", err)
	}
	if after.Unread != 1 {
		t.Fatalf("unread = %d, want 1", after.Unread)
	}

	// No bound marks the whole feed.
	all, err := h.svc.MarkNotificationsRead(ctx, accountapi.MarkNotificationsReadRequest{ActorID: owner.Account.ID})
	if err != nil {
		t.Fatalf("MarkNotificationsRead (all): %v", err)
	}
	if all.Unread != 0 {
		t.Fatalf("unread = %d, want 0", all.Unread)
	}
}

// The client is handed every pair with the value in force, so it never has to know the
// defaults — and a pair set back to its default stops being a stored row.
func TestNotificationPreferences_ResolveAndSparseStore(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	owner := h.register(t, registerRequest())

	all, err := h.svc.GetNotificationPreferences(ctx, accountapi.GetNotificationPreferencesRequest{ActorID: owner.Account.ID})
	if err != nil {
		t.Fatalf("GetNotificationPreferences: %v", err)
	}
	if want := len(domain.Categories) * len(domain.Channels); len(all) != want {
		t.Fatalf("len = %d, want %d", len(all), want)
	}
	for _, p := range all {
		if !p.IsDefault {
			t.Fatalf("pair %+v is not marked as a default on a fresh account", p)
		}
	}

	off := false
	updated, err := h.svc.UpdateNotificationPreferences(ctx, accountapi.UpdateNotificationPreferencesRequest{
		ActorID: owner.Account.ID,
		Items: []accountapi.PreferenceInput{
			{Category: "order", Channel: "push", IsEnabled: &off},
		},
	})
	if err != nil {
		t.Fatalf("UpdateNotificationPreferences: %v", err)
	}
	if got := findPreference(t, updated, "order", "push"); got.IsEnabled || got.IsDefault {
		t.Fatalf("pair = %+v, want disabled and explicit", got)
	}
	if stored, err := h.repo.ListPreferences(ctx, owner.Account.ID.Int64()); err != nil || len(stored) != 1 {
		t.Fatalf("stored = %+v, err = %v; want one deviation", stored, err)
	}

	// Back to the default: the row goes away rather than storing the default again.
	on := true
	if _, err := h.svc.UpdateNotificationPreferences(ctx, accountapi.UpdateNotificationPreferencesRequest{
		ActorID: owner.Account.ID,
		Items:   []accountapi.PreferenceInput{{Category: "order", Channel: "push", IsEnabled: &on}},
	}); err != nil {
		t.Fatalf("UpdateNotificationPreferences (reset): %v", err)
	}
	if stored, err := h.repo.ListPreferences(ctx, owner.Account.ID.Int64()); err != nil || len(stored) != 0 {
		t.Fatalf("stored = %+v, err = %v; want the row deleted", stored, err)
	}
}

// --- the follow graph ---

func TestFollow_SelfRefused(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())

	err := h.svc.Follow(context.Background(), accountapi.FollowRequest{
		ActorID: owner.Account.ID, TargetID: owner.Account.ID,
	})
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
}

// Following twice is the same state as following once, so a client that lost track can ask
// again.
func TestFollow_IdempotentAndCounted(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	follower := h.register(t, registerRequest())
	sellerReq := registerRequest()
	sellerReq.Email = "seller@example.com"
	seller := h.register(t, sellerReq)

	for i := 0; i < 2; i++ {
		if err := h.svc.Follow(ctx, accountapi.FollowRequest{ActorID: follower.Account.ID, TargetID: seller.Account.ID}); err != nil {
			t.Fatalf("Follow #%d: %v", i+1, err)
		}
	}
	public, err := h.svc.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{ID: seller.Account.ID})
	if err != nil {
		t.Fatalf("GetPublicAccount: %v", err)
	}
	if public.FollowerCount != 1 {
		t.Fatalf("follower_count = %d, want 1", public.FollowerCount)
	}

	following, err := h.svc.ListFollowing(ctx, accountapi.ListFollowingRequest{ActorID: follower.Account.ID, Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.Data) != 1 || following.Data[0].ID != seller.Account.ID {
		t.Fatalf("following = %+v", following.Data)
	}

	followers, err := h.svc.ListFollowers(ctx, accountapi.ListFollowersRequest{AccountID: seller.Account.ID, Page: 1, Limit: 20})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.Data) != 1 || followers.Data[0].Name != "Alice" {
		t.Fatalf("followers = %+v", followers.Data)
	}

	if err := h.svc.Unfollow(ctx, accountapi.UnfollowRequest{ActorID: follower.Account.ID, TargetID: seller.Account.ID}); err != nil {
		t.Fatalf("Unfollow: %v", err)
	}
	// Unfollowing something that is not followed is the state the caller asked for.
	if err := h.svc.Unfollow(ctx, accountapi.UnfollowRequest{ActorID: follower.Account.ID, TargetID: seller.Account.ID}); err != nil {
		t.Fatalf("second Unfollow: %v", err)
	}
}

// An unknown seller is a 404, not an empty follower list a client cannot tell from a new
// seller's.
func TestListFollowers_UnknownAccountNotFound(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())

	_, err := h.svc.ListFollowers(context.Background(), accountapi.ListFollowersRequest{
		AccountID: owner.Account.ID + 999, Page: 1, Limit: 20,
	})
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// The seller page is deliberately narrow: no email, no phone, no birth date.
func TestGetPublicAccount_IsNarrow(t *testing.T) {
	h := newHarness()
	owner := h.register(t, registerRequest())

	public, err := h.svc.GetPublicAccount(context.Background(), accountapi.GetPublicAccountRequest{ID: owner.Account.ID})
	if err != nil {
		t.Fatalf("GetPublicAccount: %v", err)
	}
	if public.Name != "Alice" || public.CreatedAt.IsZero() {
		t.Fatalf("public account = %+v", public)
	}
}

func findPreference(t *testing.T, rows []accountapi.NotificationPreference, category, channel string) accountapi.NotificationPreference {
	t.Helper()
	for _, p := range rows {
		if p.Category == category && p.Channel == channel {
			return p
		}
	}
	t.Fatalf("pair %s/%s is missing from %+v", category, channel, rows)
	return accountapi.NotificationPreference{}
}

// phoneCode reads the code the mock notifier "sent", which is the only way a test can
// complete the flow without a real SMS provider.
func (h *harness) phoneCode(t *testing.T, contactID int64) string {
	t.Helper()
	code, ok := h.storedPhoneCode(contactID)
	if !ok {
		t.Fatalf("no code was stored for contact %d", contactID)
	}
	return code
}

// seedNotifications writes rows straight into the repository — the feed is filled by other
// modules' events, not by this API — and returns their instants oldest-first.
func (h *harness) seedNotifications(accountID int64, n int) []time.Time {
	base := time.Now().Add(-time.Hour)
	out := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		h.repo.notifs = append(h.repo.notifs, domain.Notification{
			ID:        int64(i + 1),
			AccountID: accountID,
			Category:  domain.CategoryOrder,
			Title:     "Đơn hàng đã được xác nhận",
			Payload:   map[string]any{"order_id": "ord_1"},
			CreatedAt: at,
		})
		out = append(out, at)
	}
	return out
}
