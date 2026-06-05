package catalogbiz

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/guregu/null/v6"
	"github.com/samber/lo"

	catalogdb "shopnexus-server/internal/module/catalog/db/sqlc"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	"shopnexus-server/internal/shared/htmlutil"
)

const (
	MetadataSyncBatchSize  = 1000
	EmbeddingSyncBatchSize = 32
)

// embeddingResult holds dense and sparse vectors returned from the LLM.
type embeddingResult struct {
	dense  []float32
	sparse map[uint32]float32
}

// SetupCron starts background goroutines for metadata and embedding sync loops.
func (b *CatalogHandler) SetupCron() error {
	metadataInterval := b.cfg.Search.MetadataSyncInterval
	if metadataInterval <= 0 {
		metadataInterval = time.Second
	}

	embeddingInterval := b.cfg.Search.EmbeddingSyncInterval
	if embeddingInterval <= 0 {
		embeddingInterval = time.Second
	}

	go b.startSyncCron(context.Background(), metadataInterval, true)
	go b.startSyncCron(context.Background(), embeddingInterval, false)
	return nil
}

// startSyncCron runs a ticker loop that syncs stale entities on each tick.
func (b *CatalogHandler) startSyncCron(ctx context.Context, interval time.Duration, metadataOnly bool) {
	kind := "embedding"
	if metadataOnly {
		kind = "metadata"
	}
	log.Printf("Starting %s sync cron job...", kind)

	// Run immediately on startup
	if err := b.syncStaleEntities(ctx, metadataOnly); err != nil {
		log.Printf("Initial %s sync failed: %v", kind, err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			log.Printf("Stopping %s sync cron job...", kind)
			return
		}

		b.syncLock.Lock()
		if err := b.syncStaleEntities(ctx, metadataOnly); err != nil {
			log.Printf("%s sync failed: %v", kind, err)
		}
		b.syncLock.Unlock()
	}
}

// syncStaleEntities fetches all stale search_sync rows (regardless of ref_type),
// groups them, and dispatches to the appropriate per-entity sync function.
func (b *CatalogHandler) syncStaleEntities(ctx context.Context, metadataOnly bool) error {
	batchSize := int32(EmbeddingSyncBatchSize)
	if metadataOnly {
		batchSize = int32(MetadataSyncBatchSize)
	}

	params := catalogdb.ListStaleSearchSyncParams{
		Limit: batchSize,
	}
	if metadataOnly {
		params.IsStaleMetadata = null.BoolFrom(true)
	} else {
		params.IsStaleEmbedding = null.BoolFrom(true)
	}

	stales, err := b.storage.Querier().ListStaleSearchSync(ctx, params)
	if err != nil {
		return fmt.Errorf("list stale search sync: %w", err)
	}
	if len(stales) == 0 {
		return nil
	}

	// Group by ref_type
	grouped := make(map[catalogdb.CatalogSearchSyncRefType][]catalogdb.ListStaleSearchSyncRow)
	for _, s := range stales {
		grouped[s.RefType] = append(grouped[s.RefType], s)
	}

	for refType, rows := range grouped {
		switch refType {
		case catalogdb.CatalogSearchSyncRefTypeProductSpu:
			if err := b.syncProducts(ctx, rows, metadataOnly); err != nil {
				b.logger.Error("sync products", "error", err)
			}
		case catalogdb.CatalogSearchSyncRefTypeCategory:
			if err := b.syncCategories(ctx, rows, metadataOnly); err != nil {
				b.logger.Error("sync categories", "error", err)
			}
		case catalogdb.CatalogSearchSyncRefTypeTag:
			if err := b.syncTags(ctx, rows, metadataOnly); err != nil {
				b.logger.Error("sync tags", "error", err)
			}
		}
	}

	return nil
}

// syncProducts fetches product details directly from DB, embeds if needed,
// upserts to Milvus, and clears stale flags.
func (b *CatalogHandler) syncProducts(
	ctx context.Context,
	stales []catalogdb.ListStaleSearchSyncRow,
	metadataOnly bool,
) error {
	log.Printf("Syncing %d stale products (metadataOnly=%v)...", len(stales), metadataOnly)

	// Batch-fetch product details directly from DB (avoids N+1 HTTP via Restate ingress)
	spuIDs := make([]uuid.UUID, len(stales))
	for i, s := range stales {
		spuIDs[i] = s.RefID
	}

	dbSpus, err := b.storage.Querier().ListProductSpu(ctx, catalogdb.ListProductSpuParams{
		ID: spuIDs,
	})
	if err != nil {
		return fmt.Errorf("list spus for sync: %w", err)
	}

	dbSkus, err := b.storage.Querier().ListProductSku(ctx, catalogdb.ListProductSkuParams{
		SpuID: spuIDs,
	})
	if err != nil {
		return fmt.Errorf("list skus for sync: %w", err)
	}
	skusBySpuID := lo.GroupBy(dbSkus, func(s catalogdb.CatalogProductSku) uuid.UUID { return s.SpuID })

	tags, err := b.storage.Querier().ListProductSpuTag(ctx, catalogdb.ListProductSpuTagParams{SpuID: spuIDs})
	if err != nil {
		return fmt.Errorf("list tags for sync: %w", err)
	}
	tagsBySpuID := lo.GroupByMap(
		tags,
		func(t catalogdb.CatalogProductSpuTag) (uuid.UUID, string) { return t.SpuID, t.Tag },
	)

	categoryIDs := lo.Uniq(lo.Map(dbSpus, func(s catalogdb.CatalogProductSpu, _ int) uuid.UUID { return s.CategoryID }))
	categories, err := b.storage.Querier().ListCategory(ctx, catalogdb.ListCategoryParams{ID: categoryIDs})
	if err != nil {
		return fmt.Errorf("list categories for sync: %w", err)
	}
	categoryMap := lo.KeyBy(categories, func(c catalogdb.CatalogCategory) uuid.UUID { return c.ID })

	var products []catalogmodel.ProductDetail
	for _, spu := range dbSpus {
		skuDetails := make([]catalogmodel.ProductDetailSku, 0, len(skusBySpuID[spu.ID]))
		for _, sku := range skusBySpuID[spu.ID] {
			var attrs []catalogmodel.ProductAttribute
			sonic.Unmarshal(sku.Attributes, &attrs)
			skuDetails = append(skuDetails, catalogmodel.ProductDetailSku{
				ID:         sku.ID,
				Price:      sku.Price,
				Attributes: attrs,
			})
		}
		products = append(products, catalogmodel.ProductDetail{
			ID:          spu.ID,
			SellerID:    spu.AccountID,
			Name:        spu.Name,
			Description: spu.Description,
			IsEnabled:   spu.IsEnabled,
			Category:    mapCategory(categoryMap[spu.CategoryID]),
			Skus:        skuDetails,
			Tags:        tagsBySpuID[spu.ID],
		})
	}

	if len(products) == 0 {
		return nil
	}

	// Embed if not metadata-only
	var embeddingMap map[string]embeddingResult
	if !metadataOnly {
		texts := make([]string, len(products))
		for i, p := range products {
			texts[i] = buildEmbeddingText(p)
		}
		embeddings, err := b.llm.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed products: %w", err)
		}
		embeddingMap = make(map[string]embeddingResult, len(products))
		for i, p := range products {
			embeddingMap[p.ID.String()] = embeddingResult{
				dense:  embeddings[i].Dense,
				sparse: embeddings[i].Sparse,
			}
		}
	}

	// Upsert to Milvus
	if err := b.upsertProducts(ctx, products, embeddingMap, metadataOnly); err != nil {
		return fmt.Errorf("upsert products: %w", err)
	}

	// Clear stale flags
	return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
}

// syncCategories embeds categories and upserts to the categories collection.
// Categories have no scalar metadata in Milvus, so metadata-only syncs just clear flags.
func (b *CatalogHandler) syncCategories(
	ctx context.Context,
	stales []catalogdb.ListStaleSearchSyncRow,
	metadataOnly bool,
) error {
	if metadataOnly {
		return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
	}

	log.Printf("Syncing %d stale categories...", len(stales))

	// Collect category UUIDs from stale rows
	categoryIDs := make([]uuid.UUID, len(stales))
	for i, s := range stales {
		categoryIDs[i] = s.RefID
	}

	// Fetch categories from DB
	categories, err := b.storage.Querier().ListCategory(ctx, catalogdb.ListCategoryParams{
		ID: categoryIDs,
	})
	if err != nil {
		return fmt.Errorf("list categories for sync: %w", err)
	}

	if len(categories) == 0 {
		return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
	}

	// Embed
	texts := make([]string, len(categories))
	for i, c := range categories {
		texts[i] = buildCategoryEmbeddingText(c.Name, c.Description)
	}
	embeddings, err := b.llm.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed categories: %w", err)
	}
	embeddingMap := make(map[string]embeddingResult, len(categories))
	for i, c := range categories {
		embeddingMap[c.ID.String()] = embeddingResult{
			dense:  embeddings[i].Dense,
			sparse: embeddings[i].Sparse,
		}
	}

	// Upsert to Milvus
	if err := b.upsertCategories(ctx, categories, embeddingMap); err != nil {
		return fmt.Errorf("upsert categories: %w", err)
	}

	return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
}

// syncTags embeds tags and upserts to the tags collection.
// Tags have no scalar metadata in Milvus, so metadata-only syncs just clear flags.
func (b *CatalogHandler) syncTags(
	ctx context.Context,
	stales []catalogdb.ListStaleSearchSyncRow,
	metadataOnly bool,
) error {
	if metadataOnly {
		return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
	}

	log.Printf("Syncing %d stale tags...", len(stales))

	// Build a set of stale ref_ids for matching
	staleRefIDs := make(map[uuid.UUID]struct{}, len(stales))
	for _, s := range stales {
		staleRefIDs[s.RefID] = struct{}{}
	}

	// Tags have string PKs but search_sync uses UUID ref_id.
	// We fetch all tags and match via uuid.NewSHA1(uuid.NameSpaceURL, []byte(tag.ID)).
	// Collect tag string IDs from stale rows by reversing the mapping — we need to fetch
	// all potentially matching tags. Since we can't reverse SHA1, fetch all tags and filter.
	tags, err := b.storage.Querier().ListTag(ctx, catalogdb.ListTagParams{})
	if err != nil {
		return fmt.Errorf("list tags for sync: %w", err)
	}

	// Filter to only stale tags
	var matchedTags []catalogdb.CatalogTag
	for _, t := range tags {
		tagUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(t.ID))
		if _, ok := staleRefIDs[tagUUID]; ok {
			matchedTags = append(matchedTags, t)
		}
	}

	if len(matchedTags) == 0 {
		return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
	}

	// Embed
	texts := make([]string, len(matchedTags))
	for i, t := range matchedTags {
		texts[i] = buildTagEmbeddingText(t.ID, t.Name, t.Description.String)
	}
	embeddings, err := b.llm.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed tags: %w", err)
	}
	embeddingMap := make(map[string]embeddingResult, len(matchedTags))
	for i, t := range matchedTags {
		tagUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(t.ID))
		embeddingMap[tagUUID.String()] = embeddingResult{
			dense:  embeddings[i].Dense,
			sparse: embeddings[i].Sparse,
		}
	}

	// Upsert to Milvus
	if err := b.upsertTags(ctx, matchedTags, embeddingMap); err != nil {
		return fmt.Errorf("upsert tags: %w", err)
	}

	return b.clearStaleFlagsByRows(ctx, stales, metadataOnly)
}

// clearStaleFlagsByRows builds batch update args and clears the appropriate stale flags.
// If metadataOnly, sets is_stale_metadata=false but keeps is_stale_embedding as-is.
// If !metadataOnly, sets is_stale_embedding=false but keeps is_stale_metadata as-is.
func (b *CatalogHandler) clearStaleFlagsByRows(
	ctx context.Context,
	stales []catalogdb.ListStaleSearchSyncRow,
	metadataOnly bool,
) error {
	args := make([]catalogdb.UpdateBatchStaleSearchSyncParams, len(stales))
	for i, s := range stales {
		arg := catalogdb.UpdateBatchStaleSearchSyncParams{
			RefType: s.RefType,
			RefID:   s.RefID,
		}
		if metadataOnly {
			arg.IsStaleMetadata = null.BoolFrom(false)
			// is_stale_embedding stays as zero value (null.Bool{}) — no change
		} else {
			arg.IsStaleEmbedding = null.BoolFrom(false)
			// is_stale_metadata stays as zero value (null.Bool{}) — no change
		}
		args[i] = arg
	}
	return b.batchClearFlags(ctx, args)
}

// batchClearFlags executes the batch update for stale flags.
func (b *CatalogHandler) batchClearFlags(ctx context.Context, args []catalogdb.UpdateBatchStaleSearchSyncParams) error {
	var updateErr error
	b.storage.Querier().UpdateBatchStaleSearchSync(ctx, args).Exec(func(i int, err error) {
		if err != nil {
			updateErr = err
		}
	})
	if updateErr != nil {
		return fmt.Errorf("clear stale flags: %w", updateErr)
	}
	return nil
}

// buildEmbeddingText produces a natural-language text for embedding.
// Written as prose rather than structured labels because MGTE/BGE-M3 are trained
// on web text — they understand natural sentences, not key-value delimiters.
// The order is intentional: name first (strongest signal), then contextual keywords
// (category, tags, attributes, specs), then description last (longest, dilutes less).
func buildEmbeddingText(p catalogmodel.ProductDetail) string {
	var b strings.Builder

	b.WriteString(p.Name)

	if p.Category.Name != "" {
		b.WriteString(". ")
		b.WriteString(p.Category.Name)
	}

	if len(p.Tags) > 0 {
		b.WriteString(". ")
		b.WriteString(strings.Join(p.Tags, ", "))
	}

	// Collect unique attribute values across SKUs (e.g. "Red, Blue, XL, M")
	attrSet := make(map[string][]string)
	for _, sku := range p.Skus {
		for _, attr := range sku.Attributes {
			attrSet[attr.Name] = appendUnique(attrSet[attr.Name], attr.Value)
		}
	}
	for name, values := range attrSet {
		b.WriteString(". ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(strings.Join(values, " "))
	}

	for _, s := range p.Specifications {
		b.WriteString(". ")
		b.WriteString(s.Name)
		b.WriteString(" ")
		b.WriteString(s.Value)
	}

	desc := htmlutil.StripHTML(p.Description)
	if desc != "" {
		b.WriteString(". ")
		b.WriteString(desc)
	}

	return b.String()
}

// buildCategoryEmbeddingText creates embedding text for a category.
func buildCategoryEmbeddingText(name, description string) string {
	if description == "" {
		return name
	}
	return name + ". " + description
}

// buildTagEmbeddingText creates embedding text for a tag.
func buildTagEmbeddingText(id, name, description string) string {
	return id + ". " + name + ". " + description
}

func appendUnique(slice []string, val string) []string {
	if slices.Contains(slice, val) {
		return slice
	}
	return append(slice, val)
}
