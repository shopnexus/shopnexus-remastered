package product

import (
	"context"

	"shopnexus-remastered/internal/model"
	"shopnexus-remastered/internal/service/storage"
)

type TagResponse struct {
	model.Tag
	ProductCount int32
}

type ListTagsParams struct {
	model.PaginationParams
	Tag         *string
	Description *string
}

func (s *ServiceImpl) GetTag(ctx context.Context, tag string) (TagResponse, error) {
	tagModel, err := s.storage.GetTag(ctx, tag)
	if err != nil {
		return TagResponse{}, err
	}

	count, err := s.storage.CountProductModelsOnTag(ctx, tag)
	if err != nil {
		return TagResponse{}, err
	}

	return TagResponse{
		Tag:          tagModel,
		ProductCount: int32(count),
	}, nil
}

func (s *ServiceImpl) ListTags(ctx context.Context, params ListTagsParams) (result model.PaginateResult[TagResponse], err error) {
	count, err := s.storage.CountTags(ctx, storage.ListTagsParams{
		PaginationParams: params.PaginationParams,
		Tag:              params.Tag,
		Description:      params.Description,
	})
	if err != nil {
		return result, err
	}

	data, err := s.storage.ListTags(ctx, storage.ListTagsParams{
		PaginationParams: params.PaginationParams,
		Tag:              params.Tag,
		Description:      params.Description,
	})
	if err != nil {
		return result, err
	}

	result.Data = make([]TagResponse, 0, len(data))
	for _, d := range data {
		count, err := s.storage.CountProductModelsOnTag(ctx, d.Tag)
		if err != nil {
			return result, err
		}

		result.Data = append(result.Data, TagResponse{
			Tag:          d,
			ProductCount: int32(count),
		})
	}

	return model.PaginateResult[TagResponse]{
		Data:  result.Data,
		Total: count,
		Page:  params.Page,
		Limit: params.Limit,
	}, nil
}

func (s *ServiceImpl) CreateTag(ctx context.Context, tag model.Tag) error {
	return s.storage.CreateTag(ctx, tag)
}

type UpdateTagParams struct {
	Tag         string
	NewTag      *string
	Description *string
}

func (s *ServiceImpl) UpdateTag(ctx context.Context, params UpdateTagParams) error {
	return s.storage.UpdateTag(ctx, storage.UpdateTagParams{
		Tag:         params.Tag,
		NewTag:      params.NewTag,
		Description: params.Description,
	})
}

func (s *ServiceImpl) DeleteTag(ctx context.Context, tag string) error {
	return s.storage.DeleteTag(ctx, tag)
}
