package account_test

import (
	"context"
	"testing"
	"time"

	accountapi "shopnexus/internal/module/account/api"
)

// The two-step upload, and the reason it is two steps: a slot on its own renders nothing, so
// a profile can never show an avatar whose bytes never arrived.
func TestUpload_ConfirmedBeforeItRenders(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	user := h.register(t, registerRequest())

	slot, err := h.svc.CreateUpload(ctx, accountapi.CreateUploadRequest{
		ActorID: user.Account.ID, Kind: "avatar", Filename: "face.jpg", Mime: "image/jpeg", Size: 2048,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if slot.URL == "" || slot.ResourceID == 0 || !slot.ExpiresAt.After(time.Now()) {
		t.Fatalf("slot = %+v, want somewhere to PUT and a future expiry", slot)
	}

	// Setting it before it is confirmed is not refused — the profile just degrades to no
	// avatar, the same as a resource id that never existed.
	avatarID := slot.ResourceID
	if _, err := h.svc.UpdateProfile(ctx, accountapi.UpdateProfileRequest{
		ActorID: user.Account.ID, AvatarResourceID: &avatarID,
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	me, err := h.svc.GetMe(ctx, accountapi.GetMeRequest{ActorID: user.Account.ID})
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if me.Profile.Avatar != nil {
		t.Fatalf("avatar = %+v, want none for an unconfirmed upload", me.Profile.Avatar)
	}

	// And confirming before the bytes are there is refused too, rather than producing a row
	// that renders as a broken image.
	if err := mustErr(h.svc.ConfirmUpload(ctx, accountapi.ConfirmUploadRequest{
		ActorID: user.Account.ID, ID: slot.ResourceID,
	})); err == nil {
		t.Fatal("an upload was confirmed before anything was uploaded")
	}

	// The client PUTs, then confirms.
	h.uploads.arrived[slot.ResourceID.Int64()] = true
	res, err := h.svc.ConfirmUpload(ctx, accountapi.ConfirmUploadRequest{
		ActorID: user.Account.ID, ID: slot.ResourceID,
	})
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if res.ID != slot.ResourceID {
		t.Fatalf("confirmed = %+v, want the slot's own resource", res)
	}

	// Now the profile renders it with a link rather than a bare id.
	me, err = h.svc.GetMe(ctx, accountapi.GetMeRequest{ActorID: user.Account.ID})
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if me.Profile.Avatar == nil || me.Profile.Avatar.URL == "" {
		t.Fatalf("avatar = %+v, want a signed link on it", me.Profile.Avatar)
	}

	// Somebody else's slot is not theirs to confirm: a resource id is guessable.
	other := h.register(t, func() accountapi.RegisterRequest {
		r := registerRequest()
		r.Email, r.Name = "other@example.com", "Other"
		return r
	}())
	otherSlot, err := h.svc.CreateUpload(ctx, accountapi.CreateUploadRequest{
		ActorID: user.Account.ID, Kind: "avatar", Filename: "back.jpg", Mime: "image/jpeg", Size: 1024,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	h.uploads.arrived[otherSlot.ResourceID.Int64()] = true
	if err := mustErr(h.svc.ConfirmUpload(ctx, accountapi.ConfirmUploadRequest{
		ActorID: other.Account.ID, ID: otherSlot.ResourceID,
	})); err == nil {
		t.Fatal("a stranger confirmed somebody else's upload")
	}
}
