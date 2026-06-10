package commonbiz

import (
	"context"
	"fmt"
	"slices"

	accountmodel "shopnexus-server/internal/module/account/model"
	commondb "shopnexus-server/internal/module/common/db/sqlc"
	commonmodel "shopnexus-server/internal/module/common/model"
	"shopnexus-server/internal/shared/validator"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
	"github.com/samber/lo"
)

type UpdateResourcesParams struct {
	Account         accountmodel.AuthenticatedAccount
	RefType         commondb.CommonResourceRefType `validate:"required,validateFn=Valid"`
	RefID           uuid.UUID                      `validate:"required,gt=0"`
	ResourceIDs     []uuid.UUID                    `validate:"omitempty,dive"` // nil with EmptyResources=true means to remove all resources , nil with EmptyResources=false means no-op
	EmptyResources  bool                           `validate:"omitempty"`
	DeleteResources bool                           `validate:"omitempty"`
}

// UpdateResources replaces all resource references for a given entity and returns the updated list.
func (b *CommonHandler) UpdateResources(
	ctx restate.Context,
	params UpdateResourcesParams,
) ([]commonmodel.Resource, error) {
	if err := validator.Validate(params); err != nil {
		return nil, fmt.Errorf("validate update resources: %w", err)
	}

	// execution: replace references (delete all + re-attach) in one durable step.
	if len(params.ResourceIDs) > 0 || params.EmptyResources {
		if err := restate.RunVoid(ctx, func(rctx restate.RunContext) error {
			// First step: Delete all attached resources
			if err := b.deleteResources(rctx, DeleteResourcesParams{
				RefType:             params.RefType,
				RefID:               []uuid.UUID{params.RefID},
				DeleteResources:     params.DeleteResources,
				SkipDeleteResources: params.ResourceIDs,
			}); err != nil {
				return fmt.Errorf("update resources: %w", err)
			}

			// Next step: Attach resources
			var createResourceArgs []commondb.CreateCopyDefaultResourceReferenceParams

			res, err := b.storage.Querier().ListResource(rctx, commondb.ListResourceParams{
				Id: params.ResourceIDs,
			})
			if err != nil {
				return fmt.Errorf("db update resources: %w", err)
			}
			resources := res.Data
			if len(resources) != len(params.ResourceIDs) {
				// Some resources not found or not belong to the user
				return commonmodel.ErrResourceNotFound
			}

			for order, rsID := range params.ResourceIDs {
				createResourceArgs = append(createResourceArgs, commondb.CreateCopyDefaultResourceReferenceParams{
					RsID:    rsID,
					RefType: params.RefType,
					RefID:   params.RefID,
					Order:   int32(order),
				})
			}

			if _, err = b.storage.Querier().CreateCopyDefaultResourceReference(rctx, createResourceArgs); err != nil {
				return fmt.Errorf("db update resources: %w", err)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	// tail: read back the updated resource list.
	resourcesMap, err := b.GetResources(ctx, GetResourcesParams{
		RefType: params.RefType,
		RefIDs:  []uuid.UUID{params.RefID},
	})
	if err != nil {
		return nil, fmt.Errorf("update resources: %w", err)
	}

	return resourcesMap[params.RefID], nil
}

type DeleteResourcesParams struct {
	RefType             commondb.CommonResourceRefType `validate:"required,validateFn=Valid"`
	RefID               []uuid.UUID                    `validate:"required,dive"`
	DeleteResources     bool                           `validate:"omitempty"`
	SkipDeleteResources []uuid.UUID                    `validate:"omitempty,dive"` // Skip delete resource entities with these IDs (but still remove the references)
}

// DeleteResources removes resource references and optionally deletes the underlying resource records.
func (b *CommonHandler) DeleteResources(ctx restate.Context, params DeleteResourcesParams) error {
	if err := validator.Validate(params); err != nil {
		return fmt.Errorf("validate delete resources: %w", err)
	}

	// execution: remove references (and optionally entities) in one durable step.
	return restate.RunVoid(ctx, func(rctx restate.RunContext) error {
		return b.deleteResources(rctx, params)
	})
}

// deleteResources is the context-agnostic impl, callable inside a restate.Run
// (e.g. from UpdateResources) without nesting Run calls.
func (b *CommonHandler) deleteResources(ctx context.Context, params DeleteResourcesParams) error {
	res, err := b.storage.Querier().ListResourceReference(ctx, commondb.ListResourceReferenceParams{
		RefType: []commondb.CommonResourceRefType{params.RefType},
		RefId:   params.RefID,
	})
	if err != nil {
		return fmt.Errorf("db delete resources: %w", err)
	}
	deletedResources := res.Data

	var deletedIDs []uuid.UUID
	for _, dr := range deletedResources {
		// Skip deleting resource entity if in skip list
		if !slices.Contains(params.SkipDeleteResources, dr.RsID) {
			deletedIDs = append(deletedIDs, dr.RsID)
		}
	}

	if len(deletedIDs) > 0 {
		if err := b.storage.Querier().DeleteResourceReference(ctx, commondb.DeleteResourceReferenceParams{
			RefType: []commondb.CommonResourceRefType{params.RefType},
			RefID:   params.RefID,
			RsID:    deletedIDs,
		}); err != nil {
			return fmt.Errorf("db delete resources: %w", err)
		}
	}

	if params.DeleteResources && len(deletedIDs) > 0 {
		if err := b.storage.Querier().DeleteResource(ctx, commondb.DeleteResourceParams{
			ID: deletedIDs,
		}); err != nil {
			return fmt.Errorf("db delete resources: %w", err)
		}
	}

	return nil
}

type GetResourcesParams struct {
	RefType commondb.CommonResourceRefType
	RefIDs  []uuid.UUID
}

// GetResources returns resources grouped by reference ID for the given ref type and IDs.
func (b *CommonHandler) GetResources(
	ctx context.Context,
	params GetResourcesParams,
) (map[uuid.UUID][]commonmodel.Resource, error) {
	var err error

	resources, err := b.storage.Querier().ListSortedResources(ctx, commondb.ListSortedResourcesParams{
		RefType: params.RefType,
		RefID:   params.RefIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("db list resources: %w", err)
	}

	return lo.GroupByMap(resources, func(rs commondb.ListSortedResourcesRow) (uuid.UUID, commonmodel.Resource) {
		return rs.RefID, commonmodel.Resource{
			ID:       rs.ID,
			Url:      b.mustGetFileURL(context.Background(), rs.Provider, rs.ObjectKey),
			Mime:     rs.Mime,
			Size:     rs.Size,
			Checksum: rs.Checksum,
		}
	}), nil
}

// GetResourcesByIDs returns a map of resources keyed by their IDs, falling back to placeholder URLs on error.
func (b *CommonHandler) GetResourcesByIDs(
	ctx context.Context,
	resourceIDs []uuid.UUID,
) (map[uuid.UUID]commonmodel.Resource, error) {
	result := make(map[uuid.UUID]commonmodel.Resource)
	for _, rsID := range resourceIDs {
		result[rsID] = commonmodel.Resource{
			Url: b.getPlaceholderURL(),
		}
	}

	res, err := b.storage.Querier().ListResource(ctx, commondb.ListResourceParams{
		Id: resourceIDs,
	})
	if err != nil {
		return result, nil
	}
	resources := res.Data

	for _, rs := range resources {
		result[rs.ID] = commonmodel.Resource{
			ID:       rs.ID,
			Url:      b.mustGetFileURL(context.Background(), rs.Provider, rs.ObjectKey),
			Mime:     rs.Mime,
			Size:     rs.Size,
			Checksum: rs.Checksum,
		}
	}

	return result, nil
}

func (b *CommonHandler) GetResourceByID(ctx context.Context, resourceID uuid.UUID) (*commonmodel.Resource, error) {
	resource, err := b.storage.Querier().GetResource(ctx, commondb.GetResourceParams{
		ID: uuid.NullUUID{UUID: resourceID, Valid: true},
	})
	if err != nil {
		return nil, nil
	}

	url, err := b.mustGetObjectStore(resource.Provider).GetURL(ctx, resource.ObjectKey)
	if err != nil {
		return nil, nil
	}

	return &commonmodel.Resource{
		ID:       resource.ID,
		Url:      url,
		Mime:     resource.Mime,
		Size:     resource.Size,
		Checksum: resource.Checksum,
	}, nil
}
