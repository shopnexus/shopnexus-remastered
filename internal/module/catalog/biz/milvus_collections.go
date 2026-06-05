package catalogbiz

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"

	"shopnexus-server/internal/infras/milvus"
	catalogutil "shopnexus-server/internal/module/catalog/util"
)

const (
	CollectionProducts   = "products"
	CollectionAccounts   = "accounts"
	CollectionCategories = "categories"
	CollectionTags       = "tags"
	ContentVectorDim     = 768 // 1024 - BGE-M3, 768 - MGTE
)

func productsSchema() *entity.Schema {
	return entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("number").WithDataType(entity.FieldTypeInt64)).
		WithField(entity.NewField().WithName("account_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36)).
		WithField(entity.NewField().WithName("category_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPartitionKey(true)).
		WithField(entity.NewField().WithName("is_active").WithDataType(entity.FieldTypeBool)).
		WithField(entity.NewField().WithName("price_min").WithDataType(entity.FieldTypeFloat)).
		WithField(entity.NewField().WithName("price_max").WithDataType(entity.FieldTypeFloat)).
		WithField(entity.NewField().WithName("date_created").WithDataType(entity.FieldTypeInt64)).
		WithField(entity.NewField().WithName("tags").WithDataType(entity.FieldTypeArray).WithElementType(entity.FieldTypeVarChar).WithMaxLength(100).WithMaxCapacity(50)).
		WithField(entity.NewField().WithName("sparse_vector").WithDataType(entity.FieldTypeSparseVector)).
		WithField(entity.NewField().WithName("content_vector").WithDataType(entity.FieldTypeFloatVector).WithDim(ContentVectorDim))
}

func productsIndexes() []milvus.IndexDef {
	return []milvus.IndexDef{
		// Vector indexes
		{FieldName: "sparse_vector", IndexType: index.SparseInverted, MetricType: entity.IP},
		{FieldName: "content_vector", IndexType: index.AUTOINDEX, MetricType: entity.COSINE},
		// Scalar indexes for filtering
		{FieldName: "account_id", IndexType: index.AUTOINDEX},
		{FieldName: "category_id", IndexType: index.AUTOINDEX},
		{FieldName: "is_active", IndexType: index.AUTOINDEX},
		{FieldName: "price_min", IndexType: index.AUTOINDEX},
		{FieldName: "price_max", IndexType: index.AUTOINDEX},
		{FieldName: "date_created", IndexType: index.AUTOINDEX},
	}
}

func categoriesSchema() *entity.Schema {
	return entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("sparse_vector").WithDataType(entity.FieldTypeSparseVector)).
		WithField(entity.NewField().WithName("content_vector").WithDataType(entity.FieldTypeFloatVector).WithDim(ContentVectorDim))
}

func categoriesIndexes() []milvus.IndexDef {
	return []milvus.IndexDef{
		{FieldName: "sparse_vector", IndexType: index.SparseInverted, MetricType: entity.IP},
		{FieldName: "content_vector", IndexType: index.AUTOINDEX, MetricType: entity.COSINE},
	}
}

func tagsSchema() *entity.Schema {
	return entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(100).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("sparse_vector").WithDataType(entity.FieldTypeSparseVector)).
		WithField(entity.NewField().WithName("content_vector").WithDataType(entity.FieldTypeFloatVector).WithDim(ContentVectorDim))
}

func tagsIndexes() []milvus.IndexDef {
	return []milvus.IndexDef{
		{FieldName: "sparse_vector", IndexType: index.SparseInverted, MetricType: entity.IP},
		{FieldName: "content_vector", IndexType: index.AUTOINDEX, MetricType: entity.COSINE},
	}
}

func accountsSchema() *entity.Schema {
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPrimaryKey(true)).
		WithField(entity.NewField().WithName("number").WithDataType(entity.FieldTypeInt64))
	for i := 1; i <= catalogutil.NumInterests; i++ {
		schema.WithField(
			entity.NewField().
				WithName(fmt.Sprintf("interest_%d", i)).
				WithDataType(entity.FieldTypeFloatVector).
				WithDim(ContentVectorDim),
		)
		schema.WithField(entity.NewField().WithName(fmt.Sprintf("strength_%d", i)).WithDataType(entity.FieldTypeFloat))
	}
	return schema
}

func accountsIndexes() []milvus.IndexDef {
	indexes := make([]milvus.IndexDef, 0, catalogutil.NumInterests)
	for i := 1; i <= catalogutil.NumInterests; i++ {
		indexes = append(indexes, milvus.IndexDef{
			FieldName:  fmt.Sprintf("interest_%d", i),
			IndexType:  index.AUTOINDEX,
			MetricType: entity.COSINE,
		})
	}
	return indexes
}

// SetupMilvusCollections creates the products and accounts collections if they don't exist.
func (b *CatalogHandler) SetupMilvusCollections(ctx context.Context) error {
	if err := b.milvus.EnsureCollection(ctx, CollectionProducts, productsSchema(), productsIndexes()); err != nil {
		return fmt.Errorf("setup products collection: %w", err)
	}
	if err := b.milvus.EnsureCollection(ctx, CollectionAccounts, accountsSchema(), accountsIndexes()); err != nil {
		return fmt.Errorf("setup accounts collection: %w", err)
	}
	if err := b.milvus.EnsureCollection(
		ctx,
		CollectionCategories,
		categoriesSchema(),
		categoriesIndexes(),
	); err != nil {
		return fmt.Errorf("setup categories collection: %w", err)
	}
	if err := b.milvus.EnsureCollection(ctx, CollectionTags, tagsSchema(), tagsIndexes()); err != nil {
		return fmt.Errorf("setup tags collection: %w", err)
	}
	return nil
}
