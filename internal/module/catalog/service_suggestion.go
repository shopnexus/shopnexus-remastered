package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/provider/llm"
	"shopnexus/internal/shared/id"
)

// What the route will look at, whatever the client sends. Bounds rather than validation rules: the
// DTO refuses more than this, and these say why the numbers are what they are.
const (
	// suggestionMaxImages is how many photos the model reads. Three is a front, a back and a
	// defect — past that a phone's photos are the same object again, at real cost per image.
	suggestionMaxImages = 3
	// suggestionMaxAudioBytes is a voice note's ceiling. A seller describes an item in a sentence
	// or two; a minute of Opus is well under this, and anything larger is a recording that was
	// left running.
	suggestionMaxAudioBytes = 1 << 20
	// suggestionMaxPrice is the sanity bound on a price the model proposes. Above it the number
	// is dropped rather than shown: a suggestion the seller has to notice is wrong is worse than
	// an empty field they have to fill.
	suggestionMaxPrice = 10_000_000_000
)

// SuggestListing reads a seller's photos and what they said about them, and answers a filled-in
// listing form. It writes nothing: the seller corrects what they disagree with and posts it through
// the ordinary create route, so nothing this model produces reaches a buyer without a human between.
//
// That is the whole design. A route that created the listing itself would make the AI the author of
// a claim about somebody else's goods — the condition, the price — and there is no version of that
// which a marketplace can stand behind.
func (s *Service) SuggestListing(ctx context.Context, req catalogapi.SuggestListingRequest) (catalogapi.ListingSuggestion, error) {
	if err := s.v.Struct(req); err != nil {
		return catalogapi.ListingSuggestion{}, err
	}
	// Gated on the same verified identity selling is gated on. A model call costs money on every
	// press, and this keeps it to accounts that have something to lose.
	if err := s.requireSeller(ctx, req.ActorID); err != nil {
		return catalogapi.ListingSuggestion{}, err
	}
	if len(req.Attachments) == 0 && strings.TrimSpace(req.Note) == "" && len(req.VoiceNote) == 0 {
		return catalogapi.ListingSuggestion{}, domain.ErrSuggestionEmpty
	}
	if len(req.VoiceNote) > suggestionMaxAudioBytes {
		return catalogapi.ListingSuggestion{}, domain.ErrVoiceNoteTooLarge
	}

	// What the seller said, first: the words go back in the answer whether or not the completion
	// then works, because a seller who can see what was heard can tell why the form is wrong.
	transcript, err := s.transcribe(ctx, req)
	if err != nil {
		return catalogapi.ListingSuggestion{}, err
	}
	images, err := s.suggestionImages(ctx, req.Attachments)
	if err != nil {
		return catalogapi.ListingSuggestion{}, err
	}
	said := strings.TrimSpace(strings.TrimSpace(req.Note) + "\n" + transcript)

	categories, err := s.repo.ListCategories(ctx)
	if err != nil {
		return catalogapi.ListingSuggestion{}, fmt.Errorf("list categories: %w", err)
	}
	answer, err := s.llm.Complete(ctx, llm.CompleteParams{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: suggestionPrompt(categories)},
			{Role: llm.RoleUser, Content: said, Images: images},
		},
		ResponseFormat: &llm.ResponseFormat{Name: "listing_suggestion", Schema: suggestionSchema},
		// Low, not zero: this is extraction, and a description that reads like a person wrote it
		// still needs a little room.
		Temperature: new(0.2),
	})
	if err != nil {
		return catalogapi.ListingSuggestion{}, fmt.Errorf("suggest listing: %w", err)
	}

	out, err := parseSuggestion(answer.Message.Content, categories)
	if err != nil {
		return catalogapi.ListingSuggestion{}, err
	}
	out.Transcript = transcript
	return out, nil
}

// transcribe turns the voice note into words. Empty when there is none — the seller may have typed
// instead, or said nothing at all and let the photos speak.
func (s *Service) transcribe(ctx context.Context, req catalogapi.SuggestListingRequest) (string, error) {
	if len(req.VoiceNote) == 0 {
		return "", nil
	}
	out, err := s.llm.Transcribe(ctx, llm.TranscribeParams{
		Audio:    req.VoiceNote,
		Mime:     req.VoiceNoteMime,
		Language: req.Language,
	})
	if err != nil {
		return "", fmt.Errorf("transcribe voice note: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// suggestionImages reads the photos the seller already uploaded. Only confirmed ones resolve, so a
// slot whose bytes never arrived is simply not among them — and the model reads at most three.
//
// Attachments may now be video, which this route must not touch: a clip is tens of megabytes, the
// model cannot read one, and pulling it into a request body to find that out costs the seller the
// wait. Filtered before the read rather than after.
func (s *Service) suggestionImages(ctx context.Context, attachments []id.ID[id.Resource]) ([]llm.Image, error) {
	keys, err := s.imageKeys(ctx, attachments)
	if err != nil {
		return nil, err
	}
	if len(keys) > suggestionMaxImages {
		keys = keys[:suggestionMaxImages]
	}
	blobs, err := s.uploads.Bytes(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("read listing photos: %w", err)
	}
	out := make([]llm.Image, 0, len(blobs))
	for _, blob := range blobs {
		out = append(out, llm.Image{Mime: blob.Mime, Data: blob.Data})
	}
	return out, nil
}

// imageKeys keeps the attachments that are pictures, in the order the seller sent them.
func (s *Service) imageKeys(ctx context.Context, attachments []id.ID[id.Resource]) ([]int64, error) {
	keys := resourceKeys(attachments)
	if len(keys) == 0 {
		return nil, nil
	}
	// Resolve rather than a read of its own: it is the module's existing view of an attachment and
	// it carries the mime, which is the only field this needs.
	found, err := s.uploads.Resolve(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("read listing photos: %w", err)
	}
	out := make([]int64, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(found[key].Mime, "image/") {
			out = append(out, key)
		}
	}
	return out, nil
}

// suggestionSchema is what the model must answer. Strict JSON rather than prose to parse: every
// field here is a form field, and a shape the caller can rely on is the difference between filling
// a form and guessing at one.
//
// `category` is the *name* from the list, not an id: an id is a token a model will happily invent,
// while a name it has just been shown is a choice it can only get wrong in ways the lookup below
// catches.
var suggestionSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["name", "description", "category", "condition", "tags", "specifications", "package_details", "price"],
  "properties": {
    "name": {"type": "string", "maxLength": 200},
    "description": {"type": "string", "maxLength": 4000},
    "category": {"type": "string"},
    "condition": {"type": "string", "enum": ["new", "used", "damaged"]},
    "tags": {"type": "array", "maxItems": 8, "items": {"type": "string"}},
    "specifications": {"type": "object", "additionalProperties": {"type": "string"}},
    "package_details": {
      "type": "object",
      "additionalProperties": false,
      "properties": {"weight_g": {"type": "integer"}},
      "required": ["weight_g"]
    },
    "price": {"type": "integer"}
  }
}`)

// suggestionPrompt is the whole instruction. The category list is inlined because the model has to
// choose from *this* marketplace's tree — a category it invents is a field the seller has to clear
// before they can post, which is worse than one left empty.
func suggestionPrompt(categories []domain.Category) string {
	var b strings.Builder
	b.WriteString(`You fill in a second-hand marketplace listing from a seller's photos and their own words.
The seller is in Vietnam: write in Vietnamese, and read prices as Vietnamese dong.

Rules:
- Describe only what you can see in the photos or what the seller said. Never invent a brand, a
  model number, a capacity, an included accessory or a warranty.
- "name" is what a buyer would search for: object, brand and model if they are visible or stated.
- "description" is two or three short sentences. Mention visible wear. Do not add sales language,
  emoji or hashtags.
- "condition": "new" only if it is clearly unused or the seller says so, "damaged" if a fault is
  visible or stated, otherwise "used".
- "tags": lowercase, no diacritics, hyphen between words, at most four. Leave empty rather than
  guessing.
- "price": the number the seller said, in dong. If they did not say a price, answer 0 — never
  estimate one.
- "package_details.weight_g": your estimate of the parcel's weight in grams, packaging included.
- "category": copy one line exactly from the list below, or answer "" if none of them fit.

Categories:
`)
	for _, c := range categories {
		b.WriteString("- ")
		b.WriteString(c.Name)
		b.WriteString("\n")
	}
	return b.String()
}

// parseSuggestion turns the model's answer into the DTO, dropping anything it cannot stand behind.
// Every field is a suggestion, so a value that fails a check becomes absent rather than an error:
// the form is still worth showing with one box empty, and a 500 because a model named a category
// that does not exist would waste the photos the seller already took.
func parseSuggestion(content string, categories []domain.Category) (catalogapi.ListingSuggestion, error) {
	var raw struct {
		Name           string            `json:"name"`
		Description    string            `json:"description"`
		Category       string            `json:"category"`
		Condition      string            `json:"condition"`
		Tags           []string          `json:"tags"`
		Specifications map[string]string `json:"specifications"`
		PackageDetails struct {
			WeightG int64 `json:"weight_g"`
		} `json:"package_details"`
		Price int64 `json:"price"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return catalogapi.ListingSuggestion{}, domain.ErrSuggestionUnusable
	}

	out := catalogapi.ListingSuggestion{
		Name:        clip(strings.TrimSpace(raw.Name), 200),
		Description: clip(strings.TrimSpace(raw.Description), 4000),
		// Empty rather than null: the contract says an array, and a client that has to nil-check a
		// required field is one the contract lied to.
		Tags: []string{},
	}
	switch domain.Condition(raw.Condition) {
	case domain.ConditionNew, domain.ConditionUsed, domain.ConditionDamaged:
		out.Condition = raw.Condition
	}
	if match, ok := categoryByName(categories, raw.Category); ok {
		out.CategoryID = new(id.Of[id.Category](match.ID))
	}
	for _, tag := range raw.Tags {
		slug := domain.SlugifyTag(tag)
		if slug == "" || len(out.Tags) >= 4 {
			continue
		}
		out.Tags = append(out.Tags, slug)
	}
	if raw.Price > 0 && raw.Price <= suggestionMaxPrice {
		out.Price = new(raw.Price)
	}
	if raw.PackageDetails.WeightG > 0 {
		out.WeightG = new(raw.PackageDetails.WeightG)
	}
	if len(raw.Specifications) > 0 {
		out.Specifications = make(map[string]any, len(raw.Specifications))
		for k, v := range raw.Specifications {
			out.Specifications[k] = v
		}
	}
	// A suggestion with no name is not one: everything else is optional on the form, and a client
	// showing an empty title has nothing to offer the seller.
	if out.Name == "" {
		return catalogapi.ListingSuggestion{}, domain.ErrSuggestionUnusable
	}
	return out, nil
}

// categoryByName matches the line the model copied back. Case- and space-insensitive, because that
// is the only way a copy goes wrong when the list was in front of it.
func categoryByName(categories []domain.Category, name string) (domain.Category, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return domain.Category{}, false
	}
	for _, c := range categories {
		if strings.ToLower(c.Name) == want {
			return c, true
		}
	}
	return domain.Category{}, false
}
