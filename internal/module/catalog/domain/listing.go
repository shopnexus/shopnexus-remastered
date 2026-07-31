package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/validation"
)

type Status string

const (
	StatusDraft   Status = "draft"
	StatusPending Status = "pending"
	StatusActive  Status = "active"
	StatusHidden  Status = "hidden"
)

type Condition string

const (
	ConditionNew     Condition = "new"
	ConditionUsed    Condition = "used"
	ConditionDamaged Condition = "damaged"
)

type PriceMode string

const (
	PriceModeFixed      PriceMode = "fixed"
	PriceModeNegotiable PriceMode = "negotiable"
)

type ShippingPaidBy string

const (
	ShippingPaidByBuyer  ShippingPaidBy = "buyer"
	ShippingPaidBySeller ShippingPaidBy = "seller"
)

// maxTags is a product rule, not a column: eleven tags is a seller gaming search.
const maxTags = 10

var (
	slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)
	slugEdges  = regexp.MustCompile(`(^-+)|(-+$)`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

// errCurrencyFormat mirrors the column CHECK. A currency the wallet cannot hold is a
// finance question; the shape is this module's.
var errCurrencyFormat = errx.NewValidationError("invalid field: currency", errx.Field{
	Field: "currency", Rule: "pattern", Message: "must be an ISO 4217 code such as VND",
})

// Listing is the aggregate root: one seller's offer, its purchasable variants and its tag
// set. Not an entry in a shared product master — two sellers listing the same phone are two
// listings, which is why the seller, the condition and the slug all sit here.
//
// Fields are exported and callers mutate them directly; Validate decides whether the result
// is legal and Save runs it before writing. The methods below are for what an assignment
// cannot do: a rule only the root sees, a change with a consequence, a fact worth recording.
type Listing struct {
	ID int64
	// Version is the optimistic lock: Save writes `WHERE version = @version`, so a command
	// built on a stale read is refused rather than overwriting what happened in between.
	Version        int64
	SellerID       int64 `validate:"required"`
	Slug           string
	Status         Status         `validate:"required,oneof=draft pending active hidden"`
	Name           string         `validate:"required,min=1,max=200"`
	Description    string         `validate:"max=20000"`
	CategoryID     int64          `validate:"required"`
	Condition      Condition      `validate:"required,oneof=new used damaged"`
	PriceMode      PriceMode      `validate:"required,oneof=fixed negotiable"`
	ShippingPaidBy ShippingPaidBy `validate:"required,oneof=buyer seller"`
	Currency       string         `validate:"required,len=3"`
	Specifications map[string]any
	Attachments    []int64
	// PendingEdit is an edit held for moderation; nil means none.
	PendingEdit  *PendingEdit
	CachedRating float64
	CachedSold   int64
	CreatedAt    time.Time
	// DeletedAt is a soft delete, distinct from StatusHidden: order history holds the ids
	// without a foreign key and has to stay resolvable.
	DeletedAt        *time.Time
	EmbeddingStaleAt *time.Time

	// Children. Pointers so Save fills an id in place; `-` because each owns its rules.
	Variants []*Variant `validate:"-"`
	Tags     []string   `validate:"-"`

	events []Event
}

type NewListingInput struct {
	Name           string
	Description    string
	Condition      Condition
	PriceMode      PriceMode
	ShippingPaidBy ShippingPaidBy
	Currency       string
	Specifications map[string]any
	Attachments    []int64
	Tags           []string
	Variants       []NewVariantInput
}

// NewListing builds the whole aggregate: the create request carries the variants inline, so
// there is no window in which a listing has nothing to sell.
func NewListing(sellerID, categoryID int64, in NewListingInput) (*Listing, error) {
	l := &Listing{
		Version:          1,
		SellerID:         sellerID,
		CategoryID:       categoryID,
		Status:           StatusDraft,
		Name:             strings.TrimSpace(in.Name),
		Description:      strings.TrimSpace(in.Description),
		Condition:        in.Condition,
		PriceMode:        in.PriceMode,
		ShippingPaidBy:   in.ShippingPaidBy,
		Currency:         strings.ToUpper(strings.TrimSpace(in.Currency)),
		Specifications:   in.Specifications,
		Attachments:      in.Attachments,
		Tags:             dedupe(in.Tags),
		EmbeddingStaleAt: ptr(time.Now()),
	}
	if l.Specifications == nil {
		l.Specifications = map[string]any{}
	}
	l.Slug = Slugify(l.Name)
	for _, vin := range in.Variants {
		v, err := NewVariant(vin)
		if err != nil {
			return nil, err
		}
		l.Variants = append(l.Variants, v)
	}
	// A card has to show something, so the first variant is featured until the seller
	// chooses otherwise.
	if len(l.Variants) > 0 {
		l.Variants[0].IsFeatured = true
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return l, nil
}

// Validate checks the whole aggregate — what makes exported children safe: a caller that
// breaks an invariant by hand is refused at the write rather than stored.
func (l *Listing) Validate() error {
	if err := validation.Default().Struct(l); err != nil {
		return validation.AsError(err)
	}
	if !currencyRe.MatchString(l.Currency) {
		return errCurrencyFormat
	}
	if len(l.Tags) > maxTags {
		return ErrTooManyTags
	}
	for _, tag := range l.Tags {
		if err := ValidateTagSlug(tag); err != nil {
			return err
		}
	}
	live := l.LiveVariants()
	if len(live) == 0 {
		return ErrNoVariant
	}
	seen := make(map[string]bool, len(live))
	featured := 0
	for _, v := range live {
		if err := v.Validate(); err != nil {
			return err
		}
		key := attributeKey(v.Attributes)
		if seen[key] {
			return ErrDuplicateVariant
		}
		seen[key] = true
		if v.IsFeatured {
			featured++
		}
	}
	if featured > 1 {
		return ErrDuplicateVariant
	}
	return nil
}

// LiveVariants is the set that counts: a soft-deleted variant is kept only so an order that
// names it can be rendered.
func (l *Listing) LiveVariants() []*Variant {
	out := make([]*Variant, 0, len(l.Variants))
	for _, v := range l.Variants {
		if v.IsLive() {
			out = append(out, v)
		}
	}
	return out
}

// --- the state machine. Publication always enters moderation; there is no draft → active.

func (l *Listing) Publish() error {
	if l.Status == StatusPending || l.Status == StatusActive {
		return ErrInvalidTransition
	}
	if len(l.LiveVariants()) == 0 {
		return ErrNoVariant
	}
	l.Status = StatusPending
	record(l, Published, StatusChange{Status: l.Status})
	return nil
}

// Approve clears whatever was awaiting a decision: a first publication, or an edit held
// against a live listing.
func (l *Listing) Approve() error {
	switch {
	case l.Status == StatusPending:
		l.Status = StatusActive
	case l.Status == StatusActive && l.PendingEdit != nil:
		if err := l.ApplyPendingEdit(); err != nil {
			return err
		}
	default:
		return ErrNotAwaitingModeration
	}
	record(l, Approved, StatusChange{Status: l.Status})
	return nil
}

// Takedown is the moderator's verdict. It also drops a held edit: whatever was under review
// is not going live.
func (l *Listing) Takedown(reason string) error {
	if l.Status != StatusPending && l.Status != StatusActive {
		return ErrInvalidTransition
	}
	l.Status = StatusHidden
	l.PendingEdit = nil
	record(l, TakenDown, Takedown{Status: l.Status, Reason: reason})
	return nil
}

// Hide is the seller taking their own listing down. It reads the same in the row as a
// takedown — who did it is in the trail — and that is safe only because publishing from
// hidden re-enters moderation.
func (l *Listing) Hide() error {
	if l.Status != StatusActive {
		return ErrInvalidTransition
	}
	l.Status = StatusHidden
	record(l, Hidden, StatusChange{Status: l.Status})
	return nil
}

// SubmitEdit writes the change straight onto a draft or a hidden listing and parks it for a
// live one: buyers keep seeing the approved version until a moderator applies it.
func (l *Listing) SubmitEdit(edit PendingEdit) error {
	if edit.IsEmpty() {
		return nil
	}
	if l.Status != StatusActive && l.Status != StatusPending {
		return l.apply(edit)
	}
	l.PendingEdit = &edit
	record(l, EditSubmitted, EditSubmission{Fields: edit.Fields()})
	return nil
}

// ApplyPendingEdit is what approval does with a held edit.
func (l *Listing) ApplyPendingEdit() error {
	if l.PendingEdit == nil {
		return ErrNotAwaitingModeration
	}
	edit := *l.PendingEdit
	if err := l.apply(edit); err != nil {
		return err
	}
	l.PendingEdit = nil
	return nil
}

// apply writes an edit onto the row. Absent leaves a field alone; there is no clearing here
// because every field it touches is NOT NULL.
func (l *Listing) apply(edit PendingEdit) error {
	if edit.Name != nil {
		l.Name = strings.TrimSpace(*edit.Name)
	}
	if edit.Description != nil {
		l.Description = strings.TrimSpace(*edit.Description)
	}
	if edit.CategoryID != nil {
		l.CategoryID = *edit.CategoryID
	}
	if edit.Condition != nil {
		l.Condition = *edit.Condition
	}
	if edit.PriceMode != nil {
		l.PriceMode = *edit.PriceMode
	}
	if edit.ShippingPaidBy != nil {
		l.ShippingPaidBy = *edit.ShippingPaidBy
	}
	if edit.Specifications != nil {
		l.Specifications = edit.Specifications
	}
	if edit.Attachments != nil {
		l.Attachments = edit.Attachments
	}
	if edit.Tags != nil {
		l.Tags = dedupe(edit.Tags)
	}
	// The name and the description are what the vector is built from, so an edit makes it
	// stale. The slug is not touched: it is fixed at creation and lives in URLs.
	l.EmbeddingStaleAt = ptr(time.Now())
	return l.Validate()
}

// --- variants: the child collection ---

func (l *Listing) Variant(variantID int64) (*Variant, error) {
	at := slices.IndexFunc(l.Variants, func(v *Variant) bool { return v.ID == variantID && v.IsLive() })
	if at < 0 {
		return nil, ErrVariantNotFound
	}
	return l.Variants[at], nil
}

func (l *Listing) AddVariant(v *Variant) error {
	if err := v.Validate(); err != nil {
		return err
	}
	v.ListingID = l.ID
	l.Variants = append(l.Variants, v)
	if len(l.LiveVariants()) == 1 {
		v.IsFeatured = true
	}
	if err := l.Validate(); err != nil {
		return err
	}
	record(l, VariantAdded, VariantChange{VariantID: v.ID})
	return nil
}

// RemoveVariant soft-deletes one, refusing the last live variant of a listing that is live
// or queued — there would be nothing to buy. The featured flag moves rather than vanishing.
func (l *Listing) RemoveVariant(variantID int64) error {
	v, err := l.Variant(variantID)
	if err != nil {
		return err
	}
	live := l.LiveVariants()
	if len(live) == 1 && (l.Status == StatusActive || l.Status == StatusPending) {
		return ErrLastVariant
	}
	v.DeletedAt = ptr(time.Now())
	v.IsFeatured = false
	if remaining := l.LiveVariants(); len(remaining) > 0 && !slices.ContainsFunc(remaining, func(x *Variant) bool { return x.IsFeatured }) {
		remaining[0].IsFeatured = true
	}
	record(l, VariantRemoved, VariantChange{VariantID: variantID})
	return nil
}

// SetFeatured moves the flag inside this listing's own set. The schema cannot express any
// other kind, so a stranger's id is simply not found.
func (l *Listing) SetFeatured(variantID int64) error {
	target, err := l.Variant(variantID)
	if err != nil {
		return err
	}
	for _, v := range l.Variants {
		v.IsFeatured = false
	}
	target.IsFeatured = true
	return nil
}

// Featured is what a card shows. Nil when every variant was removed, which only a draft can
// be in.
func (l *Listing) Featured() *Variant {
	for _, v := range l.LiveVariants() {
		if v.IsFeatured {
			return v
		}
	}
	return nil
}

// ListingSnapshot is the row as the audit log keeps it.
type ListingSnapshot struct {
	ID         int64    `json:"id"`
	Version    int64    `json:"version"`
	SellerID   int64    `json:"seller_id"`
	Slug       string   `json:"slug"`
	Status     Status   `json:"status"`
	Name       string   `json:"name"`
	CategoryID int64    `json:"category_id"`
	Currency   string   `json:"currency"`
	Tags       []string `json:"tags"`
	CachedSold int64    `json:"cached_sold"`
}

func (l *Listing) Snapshot() ListingSnapshot {
	return ListingSnapshot{
		ID: l.ID, Version: l.Version, SellerID: l.SellerID, Slug: l.Slug,
		Status: l.Status, Name: l.Name, CategoryID: l.CategoryID,
		Currency: l.Currency, Tags: l.Tags, CachedSold: l.CachedSold,
	}
}

// Slugify derives the URL-friendly form of a name. Fixed at creation: a slug lives in URLs
// and in whatever a buyer bookmarked.
func Slugify(name string) string {
	s := slugUnsafe.ReplaceAllString(strings.ToLower(name), "-")
	s = slugEdges.ReplaceAllString(s, "")
	if len(s) > 100 {
		s = strings.TrimRight(s[:100], "-")
	}
	return s
}

// dedupe keeps the order a client sent: a tag twice meant it once, and the join has a
// unique key that would refuse the second.
func dedupe(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// attributeKey is the comparison the partial unique index makes: jsonb ignores key order,
// so the Go side sorts before joining.
func attributeKey(attributes map[string]any) string {
	keys := make([]string, 0, len(attributes))
	for k := range attributes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, attributes[k])
	}
	return b.String()
}

func ptr[T any](v T) *T { return &v }
