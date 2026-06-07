package analyticconfig

import (
	"reflect"

	"shopnexus-server/config"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
)

type Config struct {
	config.Shared     `mapstructure:",squash"`
	PopularityWeights PopularityWeights `mapstructure:"popularityWeights"`
}

// PopularityWeights maps each user-interaction event to its contribution
// to a product's popularity score. Weights can be negative (e.g. ReturnProduct).
type PopularityWeights struct {
	Purchase            float64 `yaml:"purchase"                  mapstructure:"purchase"                  validate:"required"`
	AddToCart           float64 `yaml:"add_to_cart"               mapstructure:"add_to_cart"               validate:"required"`
	View                float64 `yaml:"view"                      mapstructure:"view"                      validate:"required"`
	AddToFavorites      float64 `yaml:"add_to_favorites"          mapstructure:"add_to_favorites"          validate:"required"`
	WriteReview         float64 `yaml:"write_review"              mapstructure:"write_review"              validate:"required"`
	RatingHigh          float64 `yaml:"rating_high"               mapstructure:"rating_high"               validate:"required"`
	RatingMedium        float64 `yaml:"rating_medium"             mapstructure:"rating_medium"             validate:"required"`
	AskQuestion         float64 `yaml:"ask_question"              mapstructure:"ask_question"              validate:"required"`
	ClickFromSearch     float64 `yaml:"click_from_search"         mapstructure:"click_from_search"         validate:"required"`
	ClickFromRecommend  float64 `yaml:"click_from_recommendation" mapstructure:"click_from_recommendation" validate:"required"`
	ClickFromCategory   float64 `yaml:"click_from_category"       mapstructure:"click_from_category"       validate:"required"`
	ViewSimilarProducts float64 `yaml:"view_similar_products"     mapstructure:"view_similar_products"     validate:"required"`
	ProductImpression   float64 `yaml:"product_impression"        mapstructure:"product_impression"        validate:"required"`
	CheckoutStarted     float64 `yaml:"checkout_started"          mapstructure:"checkout_started"          validate:"required"`
	RemoveFromCart      float64 `yaml:"remove_from_cart"          mapstructure:"remove_from_cart"          validate:"required"`
	ReturnProduct       float64 `yaml:"return_product"            mapstructure:"return_product"            validate:"required"`
	RefundRequested     float64 `yaml:"refund_requested"          mapstructure:"refund_requested"          validate:"required"`
	CancelOrder         float64 `yaml:"cancel_order"              mapstructure:"cancel_order"              validate:"required"`
	RatingLow           float64 `yaml:"rating_low"                mapstructure:"rating_low"                validate:"required"`
	ReportProduct       float64 `yaml:"report_product"            mapstructure:"report_product"            validate:"required"`
	Dislike             float64 `yaml:"dislike"                   mapstructure:"dislike"                   validate:"required"`
	HideItem            float64 `yaml:"hide_item"                 mapstructure:"hide_item"                 validate:"required"`
	NotInterested       float64 `yaml:"not_interested"            mapstructure:"not_interested"            validate:"required"`
	ViewBounce          float64 `yaml:"view_bounce"               mapstructure:"view_bounce"               validate:"required"`
}

// WeightMap reflects PopularityWeights into the map shape biz code needs.
func (w PopularityWeights) WeightMap() map[analyticmodel.Event]float64 {
	result := make(map[analyticmodel.Event]float64)

	v := reflect.ValueOf(w)
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)

		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}

		result[analyticmodel.Event(tag)] = v.Field(i).Float()
	}

	return result
}

func NewConfig() (*Config, error) {
	var cfg Config
	return &cfg, config.LoadModule("analytic", &cfg)
}
