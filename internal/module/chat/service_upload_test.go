package chat_test

import (
	"context"
	"testing"
	"time"

	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
)

// The two-step upload, and the reason it is two steps: a slot on its own attaches to nothing,
// so a message cannot end up rendering a photo whose bytes never arrived.
func TestUpload_ConfirmedBeforeItCanBeAttached(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	thread, err := h.svc.StartConversation(ctx, chatapi.StartConversationRequest{ActorID: alice, AccountID: bob})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}

	slot, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: alice, Filename: "photo.jpg", Mime: "image/jpeg", Size: 2048,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	if slot.URL == "" || slot.ResourceID == 0 || !slot.ExpiresAt.After(time.Now()) {
		t.Fatalf("slot = %+v, want somewhere to PUT and a future expiry", slot)
	}

	// Unconfirmed, so it names no usable upload: attaching it is refused exactly as a
	// made-up id would be.
	if got := status(t, mustErr(h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID,
		Attachments: []id.ID[id.Resource]{slot.ResourceID},
	}))); got != 404 {
		t.Fatalf("status = %d, want 404 attaching an unconfirmed upload", got)
	}
	// And confirming before the bytes are there is refused too, rather than producing a row
	// that renders as a broken image.
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: alice, ID: slot.ResourceID,
	})); err == nil {
		t.Fatal("an upload was confirmed before anything was uploaded")
	}

	// The client PUTs, then confirms.
	h.uploads.arrived[slot.ResourceID.Int64()] = true
	res, err := h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: alice, ID: slot.ResourceID,
	})
	if err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if res.ID != slot.ResourceID {
		t.Fatalf("confirmed = %+v, want the slot's own resource", res)
	}

	// Now it attaches, and the message renders it with a link rather than a bare id.
	sent, err := h.svc.SendMessage(ctx, chatapi.SendMessageRequest{
		ActorID: alice, ConversationID: thread.ID,
		Attachments: []id.ID[id.Resource]{slot.ResourceID},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(sent.Images) != 1 || sent.Images[0].URL == "" {
		t.Fatalf("images = %+v, want one with a signed link on it", sent.Images)
	}

	// Somebody else's slot is not theirs to confirm: a resource id is guessable.
	other, err := h.svc.CreateUpload(ctx, common.CreateUploadRequest{
		ActorID: alice, Filename: "back.jpg", Mime: "image/jpeg", Size: 1024,
	})
	if err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}
	h.uploads.arrived[other.ResourceID.Int64()] = true
	if err := mustErr(h.svc.ConfirmUpload(ctx, common.ConfirmUploadRequest{
		ActorID: bob, ID: other.ResourceID,
	})); err == nil {
		t.Fatal("a stranger confirmed somebody else's upload")
	}
}
