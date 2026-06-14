package promotionmodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type Promotion struct {
	ID      uuid.UUID     `json:"id"`
	Code    string        `json:"code"`
	OwnerID uuid.NullUUID `json:"owner_id"`

	Type        Type            `json:"type"`
	Title       string          `json:"title"`
	Description null.String     `json:"description"`
	IsEnabled   bool            `json:"is_enabled"`
	AutoApply   bool            `json:"auto_apply"`
	Group       string          `json:"group"`
	Priority    int32           `json:"priority"`
	Data        json.RawMessage `json:"data"`

	DateStarted time.Time `json:"date_started"`
	DateEnded   null.Time `json:"date_ended"`

	DateCreated time.Time `json:"date_created"`
	DateUpdated time.Time `json:"date_updated"`

	Refs []PromotionRef `json:"refs"`
}

type PromotionRef struct {
	RefType RefType   `json:"ref_type" validate:"required,validateFn=Valid"`
	RefID   uuid.UUID `json:"ref_id"   validate:"required"`
}

// PromoSpu is the minimal SPU shape promotion matching needs: SPU id + its
// category. Keeps CalculatePromotedPrices params lean across the Restate
// boundary (no full ProductSpu) and decouples promotion from catalog.
type PromoSpu struct {
	ID         uuid.UUID `json:"id"`
	CategoryID uuid.UUID `json:"category_id"`
}
