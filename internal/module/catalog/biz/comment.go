package catalogbiz

import (
	"context"
	"fmt"

	accountbiz "shopnexus-server/internal/module/account/biz"
	accountmodel "shopnexus-server/internal/module/account/model"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	commonbiz "shopnexus-server/internal/module/common/biz"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	"shopnexus-server/internal/shared/paginate"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"
)

type ListCommentParams struct {
	paginate.Params // Page | Cursor | Sort | Limit

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
	ctx context.Context,
	params ListCommentParams,
) (paginate.PaginateResult[catalogmodel.Comment], error) {
	var zero paginate.PaginateResult[catalogmodel.Comment]

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate list comment: %w", err)
	}

	res, err := b.storage.Querier().ListComment(ctx, catalogdb.ListCommentParams{
		Params:    params.Params,
		RefType:   []catalogdb.CatalogCommentRefType{params.RefType},
		Id:        params.ID,
		RefId:     params.RefID,
		AccountId: params.AccountID,
		ScoreFrom: params.ScoreFrom,
		ScoreTo:   params.ScoreTo,
	})
	if err != nil {
		return zero, fmt.Errorf("db list comment: %w", err)
	}
	if len(res.Data) == 0 {
		return zero, nil
	}

	accountIDs := lo.Map(res.Data, func(c catalogdb.CatalogComment, _ int) uuid.UUID { return c.AccountID })
	commentIDs := lo.Map(res.Data, func(c catalogdb.CatalogComment, _ int) uuid.UUID { return c.ID })

	listProfile, err := b.account.Guaranteed().ListProfile(ctx, accountbiz.ListProfileParams{AccountIDs: accountIDs})
	if err != nil {
		return zero, fmt.Errorf("list comment profiles: %w", err)
	}
	profileMap := lo.KeyBy(listProfile.Data, func(a accountmodel.Profile) uuid.UUID { return a.ID })

	resourcesMap, err := b.common.Guaranteed().GetResources(ctx, commonbiz.GetResourcesParams{
		RefType: commondb.CommonResourceRefTypeComment,
		RefIDs:  commentIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("list comment resources: %w", err)
	}

	comments := lo.Map(res.Data, func(c catalogdb.CatalogComment, _ int) catalogmodel.Comment {
		return catalogmodel.Comment{
			CatalogComment: c,
			Profile:        profileMap[c.AccountID],
			Resources:      resourcesMap[c.ID],
		}
	})

	// Carry pagination metadata (Total / NextCursor / PageParams) from the repo.
	return paginate.PaginateResult[catalogmodel.Comment]{
		PageParams: res.PageParams,
		Data:       comments,
		Total:      res.Total,
		NextCursor: res.NextCursor,
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

// CreateComment stores a comment with resources and tracks review analytics.
// Purchase eligibility for product reviews is validated upstream by the order
// module (Order.CreateProductReview) — catalog trusts its internal callers.
func (b *CatalogHandler) CreateComment(ctx context.Context, params CreateCommentParams) (catalogmodel.Comment, error) {
	var zero catalogmodel.Comment

	if err := validator.Validate(params); err != nil {
		return zero, fmt.Errorf("validate create comment: %w", err)
	}

	// One review per order per product.
	if params.RefType == catalogdb.CatalogCommentRefTypeProductSpu {
		existing, err := b.storage.Querier().ListComment(ctx, catalogdb.ListCommentParams{Params: paginate.Params{Limit: null.Int32From(1)},
			RefType: []catalogdb.CatalogCommentRefType{catalogdb.CatalogCommentRefTypeProductSpu},
			RefId:   []uuid.UUID{params.RefID},
			OrderId: []uuid.UUID{params.OrderID},
		})
		if err != nil {
			return zero, fmt.Errorf("check existing review: %w", err)
		}
		if len(existing.Data) > 0 {
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
	resources, err := b.common.Guaranteed().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
		Account:     params.Account,
		RefType:     commondb.CommonResourceRefTypeComment,
		RefID:       comment.ID,
		ResourceIDs: params.ResourceIDs,
	})
	if err != nil {
		return zero, fmt.Errorf("create comment: %w", err)
	}

	profile, err := b.account.Guaranteed().GetProfile(ctx, accountbiz.GetProfileParams{
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
		if err := b.analytic.Send().CreateInteraction(ctx, analyticbiz.CreateInteractionParams{
			Interactions: interactions,
		}); err != nil {
			return zero, fmt.Errorf("track review interactions: %w", err)
		}

		// Notify product seller about new review
		if spu, err := b.storage.Querier().GetProductSpu(ctx, catalogdb.GetProductSpuParams{
			ID: uuid.NullUUID{UUID: params.RefID, Valid: true},
		}); err == nil {
			if err = b.account.Guaranteed().Send().CreateNotification(ctx, accountbiz.CreateNotificationParams{
				AccountID: spu.AccountID,
				Type:      accountmodel.NotiNewReview,
				Channel:   accountmodel.ChannelInApp,
				Title:     "New review",
				Content:   "A customer left a review on your product.",
			}); err != nil {
				return zero, fmt.Errorf("notify seller: %w", err)
			}
		}
	}

	return catalogmodel.Comment{
		CatalogComment: comment,
		Profile:        profile,
		Resources:      resources,
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
func (b *CatalogHandler) UpdateComment(ctx context.Context, params UpdateCommentParams) (catalogmodel.Comment, error) {
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
	resources, err := b.common.Guaranteed().UpdateResources(ctx, commonbiz.UpdateResourcesParams{
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

	profile, err := b.account.Guaranteed().GetProfile(ctx, accountbiz.GetProfileParams{
		AccountID: comment.AccountID,
	})
	if err != nil {
		return zero, fmt.Errorf("get comment profile: %w", err)
	}

	return catalogmodel.Comment{
		CatalogComment: comment,
		Profile:        profile,
		Resources:      resources,
	}, nil
}

type DeleteCommentParams struct {
	Account accountmodel.AuthenticatedAccount

	CommentIDs []uuid.UUID `validate:"required,dive,gt=0"`
}

// DeleteComment deletes comments and their associated resources.
func (b *CatalogHandler) DeleteComment(ctx context.Context, params DeleteCommentParams) error {
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
	if err := b.common.Guaranteed().DeleteResources(ctx, commonbiz.DeleteResourcesParams{
		RefType:         commondb.CommonResourceRefTypeComment,
		RefID:           params.CommentIDs,
		DeleteResources: true,
	}); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}

	return nil
}
