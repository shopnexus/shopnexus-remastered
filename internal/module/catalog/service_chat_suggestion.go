package catalog

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"sort"
	"strings"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/provider/llm"
)

const (
	// chatQuestionsWanted is how many openers come back. Four is the most a row of chips can hold
	// before it becomes a menu to read, which is the thing they exist to save the buyer from.
	chatQuestionsWanted = 4
	// chatQuestionMaxRunes keeps a chip to one line. Vietnamese runs long, and a question that
	// wraps to three lines in a 380px dock is a paragraph with a border around it.
	chatQuestionMaxRunes = 60
	// chatDescriptionBudget is how much of the seller's own text the model reads. Enough to know
	// what has already been answered; not so much that a novel of a description costs a page of
	// tokens on every product view.
	chatDescriptionBudget = 1200
	// chatSpecificationBudget caps how many specification rows go into the prompt. They are the
	// seller's own free-form keys, so there is no upper bound in the data itself.
	chatSpecificationBudget = 20
)

// SuggestChatQuestions answers the openers a buyer would tap to start a conversation about this
// listing.
//
// The point is what it *does not* suggest. A static list asks "còn bảo hành không?" on a listing
// whose warranty is stated in the specifications, and "thương lượng được không?" on a fixed price —
// two questions the page already answered, which teach the buyer that these chips are decoration.
// The model is given the listing and told to ask only what is still open, so what comes back is
// specific to a bicycle, a phone or a winter coat.
//
// It writes nothing and remembers nothing. Every call is a fresh read: the questions are cheap,
// they change when the seller edits the listing, and a cache would be a second source of truth for
// something that is only ever a suggestion.
func (s *Service) SuggestChatQuestions(
	ctx context.Context,
	req catalogapi.SuggestChatQuestionsRequest,
) (catalogapi.ChatQuestions, error) {
	if err := s.v.Struct(req); err != nil {
		return catalogapi.ChatQuestions{}, err
	}

	// Through the ordinary read, so a listing the caller may not see is refused here for exactly
	// the reasons it is refused there — and a draft cannot be described to a stranger by a model.
	listing, err := s.GetListing(ctx, catalogapi.GetListingRequest{
		ID:       req.ListingID,
		ViewerID: req.ActorID,
	})
	if err != nil {
		return catalogapi.ChatQuestions{}, err
	}
	// Nobody opens a chat with themselves, and the questions would be about their own goods.
	if listing.Seller.ID == req.ActorID {
		return catalogapi.ChatQuestions{}, domain.ErrChatQuestionsOwnListing
	}

	answer, err := s.llm.Complete(ctx, llm.CompleteParams{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: chatQuestionsPrompt},
			{Role: llm.RoleUser, Content: chatQuestionsFacts(listing)},
		},
		ResponseFormat: &llm.ResponseFormat{Name: "chat_questions", Schema: chatQuestionsSchema},
		// Warmer than the listing extractor. This is phrasing rather than extraction, and four
		// questions at temperature zero come out as the same four questions for everything.
		Temperature: new(0.6),
		MaxTokens:   300,
	})
	if err != nil {
		return catalogapi.ChatQuestions{}, fmt.Errorf("suggest chat questions: %w", err)
	}

	questions := parseChatQuestions(answer.Message.Content)
	if len(questions) == 0 {
		return catalogapi.ChatQuestions{}, domain.ErrChatQuestionsUnusable
	}
	return catalogapi.ChatQuestions{Questions: questions}, nil
}

// chatQuestionsSchema is what the model must answer: a list of strings and nothing else. The
// caller renders them as buttons, so anything it cannot render is a chip that says "undefined".
var chatQuestionsSchema = jsontext.Value(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["questions"],
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {"type": "string", "maxLength": 120}
    }
  }
}`)

// chatQuestionsPrompt is the whole instruction. Every rule in it is there to stop one specific
// failure that makes a chip worse than no chip.
const chatQuestionsPrompt = `You write the opening messages a buyer taps to start a chat with a seller on a Vietnamese
second-hand marketplace. You are given one listing. Answer with at most four questions that buyer
would actually send.

Write in Vietnamese, in the register two strangers use to trade: "mình" for the buyer, "bạn" for
the seller. No greeting, no "chào bạn", no emoji — the chip is the whole message and it should read
like something a person typed in a hurry.

Rules:
- Never ask what the listing already answers. If the condition, the location, the warranty, the
  colour, the size or the included accessories are stated, they are settled: ask about something
  else.
- Only suggest haggling when the price is marked negotiable.
- Only suggest meeting in person when the listing says where the goods are, and name that place.
- Be specific to this object. For a phone ask about battery health or the IMEI; for a bicycle ask
  about the frame size or the brakes; for clothing ask about measurements. A question that would
  fit any listing is a wasted chip.
- Each question is one sentence, at most 60 characters, and ends with a question mark.
- Never promise anything on the seller's behalf and never state a fact about the goods. You are
  writing a question, not an answer.
- If the listing is so completely described that only one question is left, answer with one.`

// chatQuestionsFacts is the listing as the model reads it: labelled lines rather than JSON, because
// what matters here is which facts are *present*, and an explicit "khong ghi" is the signal that a
// question about that field is still worth asking.
func chatQuestionsFacts(l catalogapi.ListingDetail) string {
	var b strings.Builder

	line := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\n")
	}

	line("Tên", l.Name)
	line("Danh mục", l.Category.Name)
	line("Tình trạng", conditionText(l.Condition))
	line("Hình thức giá", priceModeText(l.PriceMode))
	if l.Location != nil {
		line("Nơi bán", locationText(*l.Location))
	}
	line("Mô tả", clip(strings.TrimSpace(l.Description), chatDescriptionBudget))

	if len(l.Variants) > 1 {
		line("Số phân loại", fmt.Sprintf("%d", len(l.Variants)))
	}
	if specs := specificationLines(l.Specifications); specs != "" {
		b.WriteString("Thông số người bán đã ghi:\n")
		b.WriteString(specs)
	}
	return b.String()
}

// specificationLines flattens the seller's free-form specification object. Sorted, so the same
// listing produces the same prompt and a difference in the answer is a difference in the model
// rather than in Go's map iteration.
func specificationLines(specs map[string]any) string {
	if len(specs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(specs))
	for key := range specs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, key := range keys {
		if i >= chatSpecificationBudget {
			break
		}
		value := strings.TrimSpace(fmt.Sprintf("%v", specs[key]))
		if value == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(clip(value, 200))
		b.WriteString("\n")
	}
	return b.String()
}

func conditionText(condition string) string {
	switch domain.Condition(condition) {
	case domain.ConditionNew:
		return "mới"
	case domain.ConditionUsed:
		return "đã qua sử dụng"
	case domain.ConditionDamaged:
		return "có lỗi / hư hỏng"
	}
	return ""
}

func priceModeText(mode string) string {
	if mode == string(domain.PriceModeNegotiable) {
		return "có thể thương lượng"
	}
	return "giá cố định, không thương lượng"
}

func locationText(location catalogapi.ListingLocation) string {
	parts := make([]string, 0, 3)
	if location.WardName != "" {
		parts = append(parts, location.WardName)
	}
	if location.DistrictName != nil && *location.DistrictName != "" {
		parts = append(parts, *location.DistrictName)
	}
	if location.ProvinceName != "" {
		parts = append(parts, location.ProvinceName)
	}
	return strings.Join(parts, ", ")
}

// parseChatQuestions keeps the answers that are usable and drops the rest, rather than failing on
// the first bad one: three good chips are still three good chips. An unparseable answer comes back
// empty and the caller turns that into the one error this route has.
func parseChatQuestions(content string) []string {
	var raw struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}

	out := make([]string, 0, chatQuestionsWanted)
	seen := make(map[string]struct{}, chatQuestionsWanted)
	for _, question := range raw.Questions {
		question = strings.TrimSpace(strings.Trim(strings.TrimSpace(question), `"`))
		if question == "" || len([]rune(question)) > chatQuestionMaxRunes {
			continue
		}
		// Case-folded, because a model that repeats itself repeats itself with a different first
		// letter, and two chips saying the same thing is the row's worst failure.
		key := strings.ToLower(question)
		if _, repeated := seen[key]; repeated {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, question)
		if len(out) == chatQuestionsWanted {
			break
		}
	}
	return out
}
