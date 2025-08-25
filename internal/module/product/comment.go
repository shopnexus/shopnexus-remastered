package product

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/storage"
)

func (s *ServiceImpl) GetComment(ctx context.Context, id int64) (model.Comment, error) {
	comment, err := s.storage.GetComment(ctx, id)
	if err != nil {
		return model.Comment{}, err
	}

	return comment, nil
}

type ListCommentsParams = storage.ListCommentsParams

func (s *ServiceImpl) ListComments(ctx context.Context, params ListCommentsParams) (result model.PaginateResult[model.Comment], err error) {
	total, err := s.storage.CountComments(ctx, params)
	if err != nil {
		return result, err
	}

	comments, err := s.storage.ListComments(ctx, params)
	if err != nil {
		return result, err
	}

	return model.PaginateResult[model.Comment]{
		Data:       comments,
		Limit:      params.Limit,
		Page:       params.Page,
		Total:      total,
		NextPage:   params.NextPage(total),
		NextCursor: nil,
	}, nil
}

type CreateCommentParams struct {
	AccountID int64
	Type      model.CommentType
	DestID    int64
	Body      string
	Resources []string
}

func (s *ServiceImpl) CreateComment(ctx context.Context, params CreateCommentParams) (model.Comment, error) {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return model.Comment{}, err
	}
	defer txStorage.Rollback(ctx)

	comment, err := txStorage.CreateComment(ctx, model.Comment{
		Type:      params.Type,
		AccountID: params.AccountID,
		DestID:    params.DestID,
		Body:      params.Body,
		Resources: params.Resources,
	})
	if err != nil {
		return model.Comment{}, err
	}

	if err := txStorage.Commit(ctx); err != nil {
		return model.Comment{}, err
	}

	return comment, nil
}

// TODO: always check user only modify their resources
type UpdateCommentParams struct {
	Role      model.AccountType
	AccountID int64
	ID        int64
	Body      *string
	Resources *[]string
}

func (s *ServiceImpl) UpdateComment(ctx context.Context, params UpdateCommentParams) error {
	txStorage, err := s.storage.Begin(ctx)
	if err != nil {
		return err
	}
	defer txStorage.Rollback(ctx)

	storageParams := storage.UpdateCommentParams{
		ID:        params.ID,
		Body:      params.Body,
		Resources: params.Resources,
	}

	// User only can modify their own comment
	if params.Role == model.RoleUser {
		storageParams.AccountID = &params.AccountID
	}

	err = txStorage.UpdateComment(ctx, storageParams)
	if err != nil {
		return err
	}

	if err := txStorage.Commit(ctx); err != nil {
		return err
	}

	return nil
}

type DeleteCommentParams struct {
	Role      model.AccountType
	AccountID int64
	ID        int64
}

func (s *ServiceImpl) DeleteComment(ctx context.Context, params DeleteCommentParams) error {
	storageParams := storage.DeleteCommentParams{
		ID: params.ID,
	}

	// User only can delete their own comment
	if params.Role == model.RoleUser {
		storageParams.AccountID = &params.AccountID
	}

	return s.storage.DeleteComment(ctx, storageParams)
}
