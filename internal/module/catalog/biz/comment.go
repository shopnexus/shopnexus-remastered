package catalogbiz

import (
	"fmt"
	"log/slog"
	"time"

	restate "github.com/restatedev/sdk-go"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/ptrutil"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type ListReviewableOrdersParams struct {
	Account accountmodel.AuthenticatedAccount
	SpuID   uuid.UUID `validate:"required"`
}

type ListCommentParams struct {
	paginate.Params

	Account   accountmodel.AuthenticatedAccount
	RefType   catalogdb.CatalogCommentRefType `validate:"required,validateFn=Valid"`
	ID        []uuid.UUID                     `validate:"omitempty,dive,gt=0"`
	AccountID []uuid.UUID                     `validate:"omitempty,dive,gt=0"`
	RefID     []uuid.UUID                     `validate:"omitempty,dive,gt=0"`
	ScoreFrom null.Float                      `validate:"omitnil,gte=0,lte=1"`
	ScoreTo   null.Float                      `validate:"omitnil,gte=0,lte=1"`
}

// ListComment returns paginated comments with author profiles and attached resources.
func (b *CatalogHandler) ListComment(
	ctx restate.Context,
	params ListCommentParams,
) (paginate.PaginateResult[catalogmodel.Comment], error) {
	var zero paginate.PaginateResult[catalogmodel.Comment]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list comment: %w", err)
	}

	listComment, err := b.storage.Querier().ListCountComment(ctx, catalogdb.ListCountCommentParams{
		Limit:     params.Limit,
		Offset:    params.Offset(),
		ID:        params.ID,
		AccountID: params.AccountID,
		RefType:   []catalogdb.CatalogCommentRefType{params.RefType},
		RefID:     params.RefID,
		ScoreFrom: params.ScoreFrom,
		ScoreTo:   params.ScoreTo,
	})
	if err != nil {
		return zero, fmt.Errorf("db list comment: %w", err)
	}

	var total null.Int64
	if len(listComment) > 0 {
		total.SetValid(listComment[0].TotalCount)
	}

	dbComments := lo.Map(listComment, func(row catalogdb.ListCountCommentRow, _ int) catalogdb.CatalogComment {
		return row.CatalogComment
	})

	var commentIDs []uuid.UUID
	var accountIDs []uuid.UUID
	for _, row := range dbComments {
		commentIDs = append(commentIDs, row.ID)
		accountIDs = append(accountIDs, row.AccountID)
	}

	// Map accounts to comments
	listProfile, err := b.account.ListProfile(ctx, accountbiz.ListProfileParams{
		Issuer:     params.Account,
		AccountIDs: accountIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list comment profiles: %w", err)
	}
	// map[accountID]catalogdb.AccountProfile
	profileMap := lo.KeyBy(listProfile.Data, func(a accountmodel.Profile) uuid.UUID { return a.ID })

	// Map resources to comments
	resourcesMap, err := b.common.GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeComment,
		RefIDs:  commentIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list comment resources: %w", err)
	}

	// Batch-fetch reply counts
	replyCountMap := map[uuid.UUID]int64{}
	if len(commentIDs) > 0 {
		replyCounts, err := b.storage.Querier().CountRepliesByCommentIDs(ctx, commentIDs)
		if err != nil {
			return zero, fmt.Errorf("count replies: %w", err)
		}
		for _, rc := range replyCounts {
			replyCountMap[rc.RefID] = rc.ReplyCount
		}
	}

	// Fetch order items for enrichment (SKU name + order date)
	var orderIDs []uuid.UUID
	for _, c := range dbComments {
		if c.OrderID.Valid {
			orderIDs = append(orderIDs, c.OrderID.UUID)
		}
	}

	type orderItemInfo struct {
		SkuName   string
		OrderDate *time.Time
	}
	orderItemMap := map[uuid.UUID]orderItemInfo{}
	if len(orderIDs) > 0 {
		type orderItem struct {
			SkuName string `json:"sku_name"`
		}
		type orderEnrich struct {
			ID          uuid.UUID   `json:"id"`
			DateCreated time.Time   `json:"date_created"`
			Items       []orderItem `json:"items"`
		}
		for _, oid := range lo.Uniq(orderIDs) {
			order, err := restate.Service[orderEnrich](ctx, "Order", "GetBuyerOrder").Request(oid)
			if err != nil {
				b.logger.Warn(
					"fetch order for comment enrichment",
					slog.String("order_id", oid.String()),
					slog.Any("error", err),
				)
				continue
			}
			orderDate := order.DateCreated
			var skuName string
			if len(order.Items) > 0 {
				skuName = order.Items[0].SkuName
			}
			orderItemMap[order.ID] = orderItemInfo{
				SkuName:   skuName,
				OrderDate: &orderDate,
			}
		}
	}

	var comments []catalogmodel.Comment
	for _, dbComment := range dbComments {
		c := catalogmodel.Comment{
			ID:          dbComment.ID,
			Profile:     profileMap[dbComment.AccountID],
			Body:        dbComment.Body,
			Upvote:      dbComment.Upvote,
			Downvote:    dbComment.Downvote,
			Score:       dbComment.Score,
			OrderID:     ptrutil.PtrIf(dbComment.OrderID.UUID, dbComment.OrderID.Valid),
			DateCreated: dbComment.DateCreated,
			DateUpdated: dbComment.DateUpdated,
			Resources:   resourcesMap[dbComment.ID],
			ReplyCount:  replyCountMap[dbComment.ID],
		}
		if dbComment.OrderID.Valid {
			if info, ok := orderItemMap[dbComment.OrderID.UUID]; ok {
				c.OrderItemName = info.SkuName
				c.OrderDate = info.OrderDate
			}
		}
		comments = append(comments, c)
	}

	return paginate.PaginateResult[catalogmodel.Comment]{
		PageParams: params.Params,
		Data:       comments,
		Total:      total,
	}, nil
}

type CreateCommentParams struct {
	Account accountmodel.AuthenticatedAccount

	RefType catalogdb.CatalogCommentRefType `validate:"required"`
	RefID   uuid.UUID                       `validate:"required"`
	Body    string                          `validate:"required,min=1,max=1000"`
	Score   float64                         `validate:"required,gte=0,lte=1"`
	OrderID uuid.UUID                       `validate:"required"`

	ResourceIDs []uuid.UUID `validate:"omitempty,dive"`
}

// CreateComment creates a new comment with resources and tracks review analytics.
// For product reviews (RefType=ProductSpu), the user must have a completed order for the product.
func (b *CatalogHandler) CreateComment(ctx restate.Context, params CreateCommentParams) (catalogmodel.Comment, error) {
	var zero catalogmodel.Comment

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create comment: %w", err)
	}

	// Verify purchase for product reviews
	if params.RefType == catalogdb.CatalogCommentRefTypeProductSpu {
		skuIDs, err := b.getSkuIDsForSpu(ctx, params.RefID)
		if err != nil {
			return zero, err
		}

		type validateParams struct {
			AccountID uuid.UUID   `json:"account_id"`
			OrderID   uuid.UUID   `json:"order_id"`
			SkuIDs    []uuid.UUID `json:"sku_ids"`
		}
		valid, err := restate.Service[bool](ctx, "Order", "ValidateOrderForReview").Request(validateParams{
			AccountID: params.Account.ID,
			OrderID:   params.OrderID,
			SkuIDs:    skuIDs,
		})
		if err != nil {
			return zero, fmt.Errorf("validate order for review: %w", err)
		}
		if !valid {
			return zero, catalogmodel.ErrMustPurchaseToReview
		}

		// Check if this order was already reviewed for this product
		existing, err := b.storage.Querier().ListCountComment(ctx, catalogdb.ListCountCommentParams{
			Limit:   null.Int32From(1),
			RefType: []catalogdb.CatalogCommentRefType{catalogdb.CatalogCommentRefTypeProductSpu},
			RefID:   []uuid.UUID{params.RefID},
			OrderID: []uuid.NullUUID{{UUID: params.OrderID, Valid: true}},
		})
		if err != nil {
			return zero, fmt.Errorf("check existing review: %w", err)
		}
		if len(existing) > 0 && existing[0].TotalCount > 0 {
			return zero, catalogmodel.ErrOrderAlreadyReviewed
		}
	}

	comment, err := b.storage.Querier().CreateDefaultComment(ctx, catalogdb.CreateDefaultCommentParams{
		AccountID: params.Account.ID,
		RefType:   params.RefType,
		RefID:     params.RefID,
		Body:      params.Body,
		Score:     params.Score,
		OrderID:   uuid.NullUUID{UUID: params.OrderID, Valid: params.OrderID != uuid.Nil},
	})
	if err != nil {
		return zero, fmt.Errorf("db create comment: %w", err)
	}

	// Attach resources
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeComment,
		RefID:       comment.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("create comment: %w", err)
	}

	profile, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{
		Issuer:    params.Account,
		AccountID: comment.AccountID,
	})
	if err != nil {
		return zero, fmt.Errorf("get comment profile: %w", err)
	}

	// Track analytic interactions for product reviews
	if params.RefType == catalogdb.CatalogCommentRefTypeProductSpu {
		refID := params.RefID.String()
		interactions := []analyticbiz.CreateInteraction{
			{
				Account:   params.Account,
				EventType: analyticmodel.EventWriteReview,
				RefType:   analyticmodel.InteractionRefTypeProduct,
				RefID:     refID,
			},
		}
		switch {
		case params.Score >= 0.8:
			interactions = append(
				interactions,
				analyticbiz.CreateInteraction{
					Account:   params.Account,
					EventType: analyticmodel.EventRatingHigh,
					RefType:   analyticmodel.InteractionRefTypeProduct,
					RefID:     refID,
				},
			)
		case params.Score >= 0.4:
			interactions = append(
				interactions,
				analyticbiz.CreateInteraction{
					Account:   params.Account,
					EventType: analyticmodel.EventRatingMedium,
					RefType:   analyticmodel.InteractionRefTypeProduct,
					RefID:     refID,
				},
			)
		default:
			interactions = append(
				interactions,
				analyticbiz.CreateInteraction{
					Account:   params.Account,
					EventType: analyticmodel.EventRatingLow,
					RefType:   analyticmodel.InteractionRefTypeProduct,
					RefID:     refID,
				},
			)
		}
		restate.ServiceSend(ctx, "Analytic", "CreateInteraction").Send(analyticbiz.CreateInteractionParams{
			Interactions: interactions,
		})

		// Notify product seller about new review
		if spu, err := b.storage.Querier().GetProductSpu(ctx, catalogdb.GetProductSpuParams{
			ID: uuid.NullUUID{UUID: params.RefID, Valid: true},
		}); err == nil {
			restate.ServiceSend(ctx, "Account", "CreateNotification").Send(accountbiz.CreateNotificationParams{
				AccountID: spu.AccountID,
				Type:      accountmodel.NotiNewReview,
				Channel:   accountmodel.ChannelInApp,
				Title:     "New review",
				Content:   "A customer left a review on your product.",
			})
		}
	}

	return catalogmodel.Comment{
		ID:          comment.ID,
		Profile:     profile,
		Body:        comment.Body,
		Upvote:      comment.Upvote,
		Downvote:    comment.Downvote,
		Score:       comment.Score,
		OrderID:     ptrutil.PtrIf(comment.OrderID.UUID, comment.OrderID.Valid),
		DateCreated: comment.DateCreated,
		DateUpdated: comment.DateUpdated,
		Resources:   resources,
	}, nil
}

type UpdateCommentParams struct {
	Account accountmodel.AuthenticatedAccount

	ID            uuid.UUID   `validate:"required"`
	Body          null.String `validate:"omitempty,min=1,max=1000"`
	Score         null.Float  `validate:"omitempty,gte=0,lte=1"`
	UpvoteDelta   null.Int64  `validate:"omitempty,ne=0"`
	DownvoteDelta null.Int64  `validate:"omitempty,ne=0"`

	ResourceIDs    []uuid.UUID `validate:"omitempty,dive"`
	EmptyResources bool
}

// UpdateComment updates a comment's body, score, votes, and attached resources.
func (b *CatalogHandler) UpdateComment(ctx restate.Context, params UpdateCommentParams) (catalogmodel.Comment, error) {
	var zero catalogmodel.Comment

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate update comment: %w", err)
	}

	// Update base comment info
	comment, err := b.storage.Querier().UpdateComment(ctx, catalogdb.UpdateCommentParams{
		ID:    params.ID,
		Body:  params.Body,
		Score: params.Score,
	})
	if err != nil {
		return zero, fmt.Errorf("db update comment: %w", err)
	}

	// Update upvote/downvote count
	if params.UpvoteDelta.Valid || params.DownvoteDelta.Valid {
		if err := b.storage.Querier().UpdateCommentUpvoteDownvote(ctx, catalogdb.UpdateCommentUpvoteDownvoteParams{
			ID:            params.ID,
			UpvoteDelta:   params.UpvoteDelta,
			DownvoteDelta: params.DownvoteDelta,
		}); err != nil {
			return zero, fmt.Errorf("db update comment upvote/downvote: %w", err)
		}
	}

	// Update resources
	resources, err := b.common.UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:         params.Account,
		RefType:         commondb.CommonResourceRefTypeComment,
		RefID:           params.ID,
		ResourceIDs:     params.ResourceIDs,
		EmptyResources:  params.EmptyResources, // User may want to remove all linked resources
		DeleteResources: true,
	})
	if err != nil {
		return zero, fmt.Errorf("update comment: %w", err)
	}

	profile, err := b.account.GetProfile(ctx, accountbiz.GetProfileParams{
		Issuer:    params.Account,
		AccountID: comment.AccountID,
	})
	if err != nil {
		return zero, fmt.Errorf("get comment profile: %w", err)
	}

	return catalogmodel.Comment{
		ID:          comment.ID,
		Profile:     profile,
		Body:        comment.Body,
		Upvote:      comment.Upvote,
		Downvote:    comment.Downvote,
		Score:       comment.Score,
		OrderID:     ptrutil.PtrIf(comment.OrderID.UUID, comment.OrderID.Valid),
		DateCreated: comment.DateCreated,
		DateUpdated: comment.DateUpdated,
		Resources:   resources,
	}, nil
}

type DeleteCommentParams struct {
	Account accountmodel.AuthenticatedAccount

	CommentIDs []uuid.UUID `validate:"required,dive,gt=0"`
}

// DeleteComment deletes comments and their associated resources.
func (b *CatalogHandler) DeleteComment(ctx restate.Context, params DeleteCommentParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate delete comment: %w", err)
	}

	// Delete base comments
	if err := b.storage.Querier().DeleteComment(ctx, catalogdb.DeleteCommentParams{
		ID: params.CommentIDs,
	}); err != nil {
		return fmt.Errorf("db delete comment: %w", err)
	}

	// Remove associated resources
	if err := b.common.DeleteResources(ctx, commonbiz.DeleteResourcesParams{
		RefType:         commondb.CommonResourceRefTypeComment,
		RefID:           params.CommentIDs,
		DeleteResources: true,
	}); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}

	return nil
}

// getSkuIDsForSpu returns all SKU IDs belonging to the given SPU.
func (b *CatalogHandler) getSkuIDsForSpu(ctx restate.Context, spuID uuid.UUID) ([]uuid.UUID, error) {
	skus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
		SpuID: []uuid.UUID{spuID},
	})
	if err != nil {
		return nil, fmt.Errorf("list product skus: %w", err)
	}
	if len(skus) == 0 {
		return nil, catalogmodel.ErrProductNotFound
	}
	return lo.Map(skus, func(sku catalogdb.CatalogProductSku, _ int) uuid.UUID {
		return sku.ID
	}), nil
}

// ListReviewableOrders returns completed orders for a product that the user can review.
func (b *CatalogHandler) ListReviewableOrders(
	ctx restate.Context,
	params ListReviewableOrdersParams,
) ([]catalogmodel.ReviewableOrder, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate list reviewable orders: %w", err)
	}

	skuIDs, err := b.getSkuIDsForSpu(ctx, params.SpuID)
	if err != nil {
		return nil, err
	}

	type listParams struct {
		AccountID uuid.UUID   `json:"account_id"`
		SkuIDs    []uuid.UUID `json:"sku_ids"`
	}
	orders, err := restate.Service[[]catalogmodel.ReviewableOrder](
		ctx,
		"Order",
		"ListReviewableOrders",
	).Request(listParams{
		AccountID: params.Account.ID,
		SkuIDs:    skuIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list reviewable orders: %w", err)
	}

	return orders, nil
}
