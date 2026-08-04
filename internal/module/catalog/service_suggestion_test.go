package catalog_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// suggestionJSON is what a well-behaved model answers.
const suggestionJSON = `{
  "name": "iPhone 12 64GB xanh",
  "description": "Máy đã qua sử dụng, pin 89%. Có vết xước nhỏ ở viền dưới.",
  "category": "Phones",
  "condition": "used",
  "tags": ["iPhone 12", "Apple"],
  "specifications": {"storage": "64GB"},
  "package_details": {"weight_g": 350},
  "price": 5000000
}`

// The whole point of the route: a photo and a sentence in, a filled-in form out — and nothing
// written. What the seller then posts is their own edit of it, through the ordinary create route.
func TestSuggestListing_FillsTheFormFromAPhotoAndASentence(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	// A category the model can name, and a confirmed photo it can read.
	category, err := newHarnessAdmin(h).svc.AdminCreateCategory(ctx,
		catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Phones"})
	if err != nil {
		t.Fatalf("AdminCreateCategory: %v", err)
	}
	const photo = id.ID[id.Resource](77)
	h.images[int64(photo)] = true

	h.models.answer = suggestionJSON
	h.models.transcript = "bán iphone 12 64gb pin 89 phần trăm, năm triệu"

	out, err := h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID:       actor,
		Attachments:   []id.ID[id.Resource]{photo},
		VoiceNote:     []byte("fake-opus-bytes"),
		VoiceNoteMime: "audio/ogg",
		Language:      "vi",
	})
	if err != nil {
		t.Fatalf("SuggestListing: %v", err)
	}

	if out.Name != "iPhone 12 64GB xanh" || out.Condition != "used" {
		t.Fatalf("suggestion = %+v, want the model's name and condition", out)
	}
	// The category comes back as one of *this* marketplace's ids, resolved from the name the model
	// copied — never a token it invented.
	if out.CategoryID == nil || *out.CategoryID != category.ID {
		t.Fatalf("category = %v, want %v", out.CategoryID, category.ID)
	}
	// Tags are slugified into the vocabulary the tag column accepts.
	if len(out.Tags) != 2 || out.Tags[0] != "iphone-12" || out.Tags[1] != "apple" {
		t.Fatalf("tags = %v, want slugs", out.Tags)
	}
	if out.Price == nil || *out.Price != 5_000_000 || out.WeightG == nil || *out.WeightG != 350 {
		t.Fatalf("price = %v weight = %v, want what the seller said and an estimate", out.Price, out.WeightG)
	}
	// The words go back so the seller can see why a field is wrong rather than guess.
	if out.Transcript != h.models.transcript {
		t.Fatalf("transcript = %q, want what was heard", out.Transcript)
	}

	// The voice note reached the transcription with its own container type and language hint.
	if h.models.audio.Mime != "audio/ogg" || h.models.audio.Language != "vi" {
		t.Fatalf("audio = %+v, want the mime and language through", h.models.audio)
	}
	// The photo reached the model, and so did the category list it has to choose from: without the
	// list it answers a category nobody has, which is a field the seller then has to clear.
	user := h.models.asked.Messages[len(h.models.asked.Messages)-1]
	if len(user.Images) != 1 || len(user.Images[0].Data) == 0 {
		t.Fatalf("images = %+v, want the photo sent to the model", user.Images)
	}
	if !strings.Contains(user.Content, "iphone 12") {
		t.Fatalf("prompt = %q, want the transcript in it", user.Content)
	}
	if system := h.models.asked.Messages[0].Content; !strings.Contains(system, "Phones") {
		t.Fatalf("system prompt has no category list: %q", system)
	}
	// And a schema, so the answer is a form rather than prose to parse.
	if h.models.asked.ResponseFormat == nil || h.models.asked.ResponseFormat.Schema == nil {
		t.Fatal("the completion did not ask for structured output")
	}

	// Nothing was written: no listing exists until the seller posts one.
	page, err := h.svc.ListListings(ctx, catalogapi.ListListingsRequest{
		ViewerID: actor, Mine: true, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListListings: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("listings = %+v, want the suggestion to have written nothing", page.Data)
	}
}

// Every field is a proposal, so one the route cannot stand behind is left empty rather than passed
// on: an invented category, a price nobody said, a condition outside the enum. A form with a blank
// box is worth showing; a wrong value the seller has to notice is not.
func TestSuggestListing_DropsWhatItCannotStandBehind(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	if _, err := newHarnessAdmin(h).svc.AdminCreateCategory(ctx,
		catalogapi.CreateCategoryRequest{ActorID: actor, Name: "Phones"}); err != nil {
		t.Fatalf("AdminCreateCategory: %v", err)
	}
	h.models.answer = `{
	  "name": "Quạt cũ",
	  "description": "Còn chạy tốt.",
	  "category": "Đồ gia dụng thông minh",
	  "condition": "refurbished",
	  "tags": ["!!!", "quạt điện"],
	  "specifications": {},
	  "package_details": {"weight_g": 0},
	  "price": 99999999999
	}`

	out, err := h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID: actor, Note: "bán quạt",
	})
	if err != nil {
		t.Fatalf("SuggestListing: %v", err)
	}
	if out.CategoryID != nil {
		t.Errorf("category = %v, want none: that name is not in this tree", out.CategoryID)
	}
	if out.Condition != "" {
		t.Errorf("condition = %q, want none: it is not one of ours", out.Condition)
	}
	if out.Price != nil {
		t.Errorf("price = %v, want none: it is past what any listing costs", *out.Price)
	}
	if out.WeightG != nil {
		t.Errorf("weight = %v, want none rather than zero", *out.WeightG)
	}
	if len(out.Tags) != 1 || out.Tags[0] != "quat-dien" {
		t.Errorf("tags = %v, want only the one that slugifies", out.Tags)
	}
	if out.Name == "" {
		t.Error("the name survived nothing, and it is the one field a form needs")
	}
}

// What the route refuses, and what it does not: nothing to look at is refused, an unverified seller
// is refused, and a model that answers rubbish is a 502 rather than a form with a blank title.
func TestSuggestListing_RefusesWhatItCannotAnswer(t *testing.T) {
	ctx := context.Background()

	unverified := newHarnessWith("user", false)
	unverified.models.answer = suggestionJSON
	if got := status(t, mustErr(unverified.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID: actor, Note: "bán cái gì đó",
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 for a seller with no verified identity", got)
	}

	h := newHarnessWith("user", true)
	h.models.answer = suggestionJSON
	// No photo, no note, no voice note.
	if got := status(t, mustErr(h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID: actor,
	}))); got != 422 {
		t.Fatalf("status = %d, want 422 with nothing to look at", got)
	}
	// A recording left running.
	if got := status(t, mustErr(h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID: actor, VoiceNote: make([]byte, 2<<20), VoiceNoteMime: "audio/ogg",
	}))); got != 413 {
		t.Fatalf("status = %d, want 413 for an oversized voice note", got)
	}

	// A model that answered prose, or a form with no name in it.
	for _, answer := range []string{"sure! here is your listing:", `{"name": ""}`} {
		h.models.answer = answer
		if got := status(t, mustErr(h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
			ActorID: actor, Note: "bán cái gì đó",
		}))); got != 502 {
			t.Fatalf("status = %d for %q, want 502", got, answer)
		}
	}

	// An unreachable model is the model's failure, not the seller's request.
	h.models.answer = suggestionJSON
	h.models.completeErr = errors.New("dial litellm: connection refused")
	err := mustErr(h.svc.SuggestListing(ctx, catalogapi.SuggestListingRequest{
		ActorID: actor, Note: "bán cái gì đó",
	}))
	if _, _, _, ok := errx.Decompose(err); ok {
		t.Fatalf("err = %v, want an uncoded failure so it surfaces as a 500", err)
	}
}
