package catalogmodel

import (
	commonmodel "shopnexus-server/internal/module/common/model"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

type ProductCard struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	SellerID    uuid.UUID `json:"seller_id"`
	CategoryID  uuid.UUID `json:"category_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsEnabled   bool      `json:"is_active"`
	Currency    string    `json:"currency"`
	DateCreated time.Time `json:"date_created"`
	DateUpdated time.Time `json:"date_updated"`
	DateDeleted null.Time `json:"date_deleted"`

	Price         int64                  `json:"price"`
	OriginalPrice int64                  `json:"original_price"`
	Rating        Rating                 `json:"rating"`
	IsFavorite    bool                   `json:"is_favorite"`
	Resources     []commonmodel.Resource `json:"resources"`
	Promotions    []ProductCardPromo     `json:"promotions"`
	Sold          int64                  `json:"sold"`
}

type ProductCardPromo struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
}
