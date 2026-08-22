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

// buyer is somebody other than the seller these harnesses list as. The route is for the other
// side of the trade, so almost everything here needs one.
const buyer = id.ID[id.Account](42)

// The route's whole job: what the listing already answers must reach the model, so the questions
// that come back are about what is still open.
func TestSuggestChatQuestions_SendsTheListingAndAnswersQuestions(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListingNamed(t, h, "Áo khoác Uniqlo size L")
	publish(t, h, listing)

	h.models.answer = `{"questions": [
		"Áo còn vết bẩn nào không bạn?",
		"Mình mặc 1m75 thì có vừa không?",
		"Bạn ship COD được không?"
	]}`

	out, err := h.svc.SuggestChatQuestions(ctx, catalogapi.SuggestChatQuestionsRequest{
		ActorID: buyer, ListingID: listing.ID,
	})
	if err != nil {
		t.Fatalf("SuggestChatQuestions: %v", err)
	}
	if len(out.Questions) != 3 || out.Questions[0] != "Áo còn vết bẩn nào không bạn?" {
		t.Fatalf("questions = %v, want the model's three", out.Questions)
	}

	// The listing reached the model. Without the name, the condition and — the point of the
	// route — the price mode, it cannot tell which questions are already settled.
	said := h.models.asked.Messages[len(h.models.asked.Messages)-1].Content
	for _, want := range []string{"Áo khoác Uniqlo size L", "đã qua sử dụng", "không thương lượng"} {
		if !strings.Contains(said, want) {
			t.Fatalf("prompt = %q, want %q in it", said, want)
		}
	}
	// And a schema, so the answer is a list of chips rather than prose to parse.
	if h.models.asked.ResponseFormat == nil || h.models.asked.ResponseFormat.Schema == nil {
		t.Fatal("the completion did not ask for structured output")
	}
}

// Three good chips are still three good chips: an answer with junk in it is trimmed rather than
// rejected. A repeat is dropped too — two chips that say the same thing is the row's worst state.
func TestSuggestChatQuestions_KeepsWhatItCanRender(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)

	h.models.answer = `{"questions": [
		"Áo còn không bạn?",
		"   ",
		"ÁO CÒN KHÔNG BẠN?",
		"Bạn có thể mô tả thật kỹ tình trạng của chiếc áo này, mọi vết bẩn, mọi đường chỉ bung, và cả mùi của nó không?",
		"Ship COD được không bạn?",
		"Mình lấy 2 cái có giảm không?",
		"Thừa ra một câu nữa"
	]}`

	out, err := h.svc.SuggestChatQuestions(ctx, catalogapi.SuggestChatQuestionsRequest{
		ActorID: buyer, ListingID: listing.ID,
	})
	if err != nil {
		t.Fatalf("SuggestChatQuestions: %v", err)
	}
	// The blank, the case-folded repeat and the one too long to fit a chip are all gone, and the
	// row stops at four.
	want := []string{
		"Áo còn không bạn?",
		"Ship COD được không bạn?",
		"Mình lấy 2 cái có giảm không?",
		"Thừa ra một câu nữa",
	}
	if len(out.Questions) != len(want) {
		t.Fatalf("questions = %v, want %v", out.Questions, want)
	}
	for i, question := range want {
		if out.Questions[i] != question {
			t.Fatalf("question %d = %q, want %q", i, out.Questions[i], question)
		}
	}
}

// Nobody opens a chat with themselves. Chat refuses the conversation, so openers for one are
// questions with nowhere to go.
func TestSuggestChatQuestions_RefusesTheSellersOwnListing(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)

	err := mustErr(h.svc.SuggestChatQuestions(context.Background(),
		catalogapi.SuggestChatQuestionsRequest{ActorID: actor, ListingID: listing.ID}))
	if got := status(t, err); got != 422 {
		t.Fatalf("status = %d, want 422", got)
	}
	// And the model was never asked, because there was nothing worth paying for.
	if h.models.asked.Messages != nil {
		t.Fatalf("model was asked %+v, want no call at all", h.models.asked.Messages)
	}
}

// A draft is the seller's own business. The questions are built by describing the listing to a
// model, so a route that answered here would describe an unpublished listing to a stranger.
func TestSuggestChatQuestions_RefusesAListingTheBuyerCannotRead(t *testing.T) {
	h := newHarnessWith("user", true)
	listing := seedListing(t, h)

	err := mustErr(h.svc.SuggestChatQuestions(context.Background(),
		catalogapi.SuggestChatQuestionsRequest{ActorID: buyer, ListingID: listing.ID}))
	if got := status(t, err); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

// An answer with nothing renderable in it is a 502 rather than an empty list: the caller has a
// fallback of its own, and it can only reach for it if it is told this failed.
func TestSuggestChatQuestions_RefusesAnUnusableAnswer(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)

	for _, answer := range []string{`not json at all`, `{"questions": []}`, `{"questions": ["   "]}`} {
		h.models.answer = answer
		err := mustErr(h.svc.SuggestChatQuestions(ctx,
			catalogapi.SuggestChatQuestionsRequest{ActorID: buyer, ListingID: listing.ID}))
		if got := status(t, err); got != 502 {
			t.Fatalf("answer %q: status = %d, want 502", answer, got)
		}
	}
}

// A model that is unreachable is reported, not swallowed. Same reason: the chips are optional,
// and the client decides that — not this route by answering an empty list that looks like a
// listing nobody could think of a question about.
func TestSuggestChatQuestions_ReportsAnUnreachableModel(t *testing.T) {
	h := newHarnessWith("user", true)
	ctx := context.Background()
	listing := seedListing(t, h)
	publish(t, h, listing)
	h.models.completeErr = errors.New("upstream is down")

	if err := mustErr(h.svc.SuggestChatQuestions(ctx,
		catalogapi.SuggestChatQuestionsRequest{ActorID: buyer, ListingID: listing.ID})); err == nil {
		t.Fatal("want an error when the model is unreachable")
	} else if errx.CodeOf(err) == "chat_questions_unusable" {
		t.Fatalf("err = %v, want the transport failure rather than an unusable answer", err)
	}
}
