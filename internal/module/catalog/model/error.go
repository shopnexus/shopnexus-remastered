package catalogmodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the catalog module.
var (
	ErrProductNotFound   = errors.NewError(http.StatusNotFound, "product_not_found", "The requested product could not be found")
	ErrSkuNotBelongToSpu = errors.NewError(
		http.StatusBadRequest,
		"sku_not_belong_to_spu",
		"The selected SKU does not belong to this product",
	)
	ErrNoEmbeddingsResult   = errors.NewError(http.StatusNotFound, "no_embeddings_result", "No embeddings returned for the query")
	ErrMustPurchaseToReview = errors.NewError(
		http.StatusForbidden,
		"must_purchase_to_review",
		"You must have a completed order for this product before leaving a review",
	)
	ErrOrderAlreadyReviewed = errors.NewError(
		http.StatusConflict,
		"order_already_reviewed",
		"You have already reviewed this product for this order",
	)
	ErrProductCurrencyMismatch = errors.NewErrorf(
		http.StatusBadRequest,
		"currency_mismatch",
		"seller in %s must price products in %s, got %s",
	)
)
