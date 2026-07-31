package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/module/catalog/port"
)

// listingColumns is every column of the root. A nullable one is scanned straight into the
// domain's pointer field: NULL arrives as nil, the same "not set" the entity uses. Enum
// columns are cast to text because the domain's types are strings.
const listingColumns = `id, version, account_id, slug, status::text, name, description,
	       category_id, condition::text, price_mode::text, shipping_paid_by::text, currency,
	       specifications, attachments, pending_edit, cached_rating, cached_sold,
	       created_at, deleted_at, embedding_stale_at`

func scanListing(row pgx.Row) (*domain.Listing, error) {
	var (
		l       domain.Listing
		pending []byte
	)
	err := row.Scan(&l.ID, &l.Version, &l.SellerID, &l.Slug, &l.Status, &l.Name, &l.Description,
		&l.CategoryID, &l.Condition, &l.PriceMode, &l.ShippingPaidBy, &l.Currency,
		&l.Specifications, &l.Attachments, &pending, &l.CachedRating, &l.CachedSold,
		&l.CreatedAt, &l.DeletedAt, &l.EmbeddingStaleAt)
	if isNoRows(err) {
		return nil, domain.ErrListingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db scan listing: %w", err)
	}
	// NULL means no edit is held — one representation of absent, which is why the column is
	// nullable rather than defaulting to an empty object.
	if len(pending) > 0 {
		var edit domain.PendingEdit
		if err := json.Unmarshal(pending, &edit); err != nil {
			return nil, fmt.Errorf("decode pending edit: %w", err)
		}
		l.PendingEdit = &edit
	}
	return &l, nil
}

// listingArgs is the whole row, so an insert and an update name the same values.
func listingArgs(l *domain.Listing) pgx.NamedArgs {
	var pending any
	if l.PendingEdit != nil {
		pending = l.PendingEdit
	}
	return pgx.NamedArgs{
		"id":                 l.ID,
		"version":            l.Version,
		"account_id":         l.SellerID,
		"slug":               l.Slug,
		"status":             string(l.Status),
		"name":               l.Name,
		"description":        l.Description,
		"category_id":        l.CategoryID,
		"condition":          string(l.Condition),
		"price_mode":         string(l.PriceMode),
		"shipping_paid_by":   string(l.ShippingPaidBy),
		"currency":           l.Currency,
		"specifications":     jsonObject(l.Specifications),
		"attachments":        int64Array(l.Attachments),
		"pending_edit":       pending,
		"cached_rating":      l.CachedRating,
		"cached_sold":        l.CachedSold,
		"embedding_stale_at": l.EmbeddingStaleAt,
	}
}

// variantColumns is qualified with the alias the loader's join uses, because the only read
// of a variant goes through that join — its stock row is part of the entity.
const variantColumns = `v.id, v.listing_id, v.price, v.attributes, v.package_details,
	       v.attachments, v.is_featured, v.created_at, v.deleted_at`

// scanVariant reads variantColumns followed by the three stock counters, in that order.
// pgx.Rows satisfies pgx.Row, so this one list serves the single-row read and the loop.
func scanVariant(row pgx.Row) (*domain.Variant, error) {
	var v domain.Variant
	err := row.Scan(&v.ID, &v.ListingID, &v.Price, &v.Attributes, &v.PackageDetails,
		&v.Attachments, &v.IsFeatured, &v.CreatedAt, &v.DeletedAt,
		&v.Stock.Quantity, &v.Stock.Reserved, &v.Stock.Sold)
	if isNoRows(err) {
		return nil, domain.ErrVariantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db scan variant: %w", err)
	}
	return &v, nil
}

func (r *Repo) GetListing(ctx context.Context, id int64) (*domain.Listing, error) {
	return loadListing(ctx, r.pool, `WHERE id = @id AND deleted_at IS NULL`,
		pgx.NamedArgs{"id": id})
}

// GetListingForSeller makes ownership part of the lookup, so another seller's listing is a
// 404 rather than a 403 — it is not theirs to know about.
func (r *Repo) GetListingForSeller(ctx context.Context, id, sellerID int64) (*domain.Listing, error) {
	return loadListing(ctx, r.pool,
		`WHERE id = @id AND account_id = @account_id AND deleted_at IS NULL`,
		pgx.NamedArgs{"id": id, "account_id": sellerID})
}

// loadListing reads the root, its live variants with their stock, and its tag slugs: three
// keyed queries for every command, which is the price of there being exactly one way in.
func loadListing(ctx context.Context, pool *pgxpool.Pool, where string, args pgx.NamedArgs) (*domain.Listing, error) {
	l, err := scanListing(pool.QueryRow(ctx, `SELECT `+listingColumns+` FROM listing `+where, args))
	if err != nil {
		return nil, err
	}
	l.Variants, err = listVariants(ctx, pool, l.ID)
	if err != nil {
		return nil, err
	}
	l.Tags, err = listListingTags(ctx, pool, l.ID)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// listVariants reads the live ones only: a soft-deleted variant is kept for order history,
// not for the aggregate's rules. Save's delete-by-negation is therefore about live rows.
func listVariants(ctx context.Context, pool *pgxpool.Pool, listingID int64) ([]*domain.Variant, error) {
	const sql = `SELECT ` + variantColumns + `, s.quantity, s.reserved, s.sold
	             FROM variant v
	             JOIN stock s ON s.variant_id = v.id
	             WHERE v.listing_id = @listing_id AND v.deleted_at IS NULL
	             ORDER BY v.id`
	rows, err := pool.Query(ctx, sql, pgx.NamedArgs{"listing_id": listingID})
	if err != nil {
		return nil, fmt.Errorf("db query variants: %w", err)
	}
	defer rows.Close()

	var out []*domain.Variant
	for rows.Next() {
		v, err := scanVariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate variants: %w", err)
	}
	return out, nil
}

func listListingTags(ctx context.Context, pool *pgxpool.Pool, listingID int64) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT tag FROM listing_tag WHERE listing_id = @listing_id ORDER BY tag`,
		pgx.NamedArgs{"listing_id": listingID})
	if err != nil {
		return nil, fmt.Errorf("db query listing tags: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("db scan listing tag: %w", err)
		}
		out = append(out, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db iterate listing tags: %w", err)
	}
	return out, nil
}

func (r *Repo) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM listing WHERE slug = @slug)`,
		pgx.NamedArgs{"slug": slug}).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("db query slug taken: %w", err)
	}
	return ok, nil
}

func (r *Repo) CreateListing(ctx context.Context, l *domain.Listing, actor int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	return r.inTx(ctx, func(tx pgx.Tx) error {
		const q = `INSERT INTO listing (account_id, slug, status, name, description, category_id,
		                       condition, price_mode, shipping_paid_by, currency, specifications,
		                       attachments, pending_edit, embedding_stale_at)
		           VALUES (@account_id, @slug, @status, @name, @description, @category_id,
		                   @condition, @price_mode, @shipping_paid_by, @currency, @specifications,
		                   @attachments, @pending_edit, @embedding_stale_at)
		           RETURNING id, version, created_at`
		err := tx.QueryRow(ctx, q, listingArgs(l)).Scan(&l.ID, &l.Version, &l.CreatedAt)
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrSlugTaken
			}
			if isRestrictViolation(err) {
				return domain.ErrCategoryNotFound
			}
			return fmt.Errorf("db insert listing: %w", err)
		}
		if err := saveVariants(ctx, tx, l); err != nil {
			return err
		}
		if err := saveListingTags(ctx, tx, l); err != nil {
			return err
		}
		if err := saveListingEvents(ctx, tx, l, actor); err != nil {
			return err
		}
		l.ClearEvents()
		return nil
	})
}

// SaveListing is the aggregate's only write. The version check serialises two concurrent
// commands: the loser rewrites nothing and is told so.
func (r *Repo) SaveListing(ctx context.Context, l *domain.Listing, actor int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	return r.inTx(ctx, func(tx pgx.Tx) error {
		const q = `UPDATE listing
		           SET status = @status, name = @name, description = @description,
		               category_id = @category_id, condition = @condition,
		               price_mode = @price_mode, shipping_paid_by = @shipping_paid_by,
		               currency = @currency, specifications = @specifications,
		               attachments = @attachments, pending_edit = @pending_edit,
		               embedding_stale_at = @embedding_stale_at, version = version + 1
		           WHERE id = @id AND version = @version AND deleted_at IS NULL`
		tag, err := tx.Exec(ctx, q, listingArgs(l))
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrSlugTaken
			}
			if isRestrictViolation(err) {
				return domain.ErrCategoryNotFound
			}
			return fmt.Errorf("db update listing: %w", err)
		}
		// Zero rows is a stale version, a missing listing or a deleted one. The distinction
		// costs a second query and changes nothing a caller does, so the conflict is the
		// answer.
		if tag.RowsAffected() == 0 {
			return domain.ErrVersionConflict
		}
		if err := saveVariants(ctx, tx, l); err != nil {
			return err
		}
		if err := saveListingTags(ctx, tx, l); err != nil {
			return err
		}
		// Bumped before the trail, so the snapshot carries the version the row now has.
		l.Version++
		if err := saveListingEvents(ctx, tx, l, actor); err != nil {
			return err
		}
		l.ClearEvents()
		return nil
	})
}

// saveVariants makes the table match the slice: insert what has no id, update what has one,
// and soft-delete the live rows that left. Soft, because order.item holds variant_id without
// a foreign key and a past order has to stay renderable.
func saveVariants(ctx context.Context, tx pgx.Tx, l *domain.Listing) error {
	keep := make([]int64, 0, len(l.Variants))
	for _, v := range l.Variants {
		if v.ID != 0 && v.IsLive() {
			keep = append(keep, v.ID)
		}
	}
	const del = `UPDATE variant SET deleted_at = now()
	             WHERE listing_id = @listing_id AND deleted_at IS NULL AND id <> ALL(@keep)`
	if _, err := tx.Exec(ctx, del, pgx.NamedArgs{"listing_id": l.ID, "keep": keep}); err != nil {
		return fmt.Errorf("db soft delete variants: %w", err)
	}
	for _, v := range l.Variants {
		if !v.IsLive() {
			continue
		}
		if v.ID == 0 {
			if err := insertVariant(ctx, tx, l.ID, v); err != nil {
				return err
			}
			continue
		}
		if err := updateVariant(ctx, tx, v); err != nil {
			return err
		}
	}
	return nil
}

func insertVariant(ctx context.Context, tx pgx.Tx, listingID int64, v *domain.Variant) error {
	const q = `INSERT INTO variant (listing_id, price, attributes, package_details, attachments,
	                        is_featured)
	           VALUES (@listing_id, @price, @attributes, @package_details, @attachments,
	                   @is_featured)
	           RETURNING id, created_at`
	v.ListingID = listingID
	if err := tx.QueryRow(ctx, q, variantArgs(v)).Scan(&v.ID, &v.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrDuplicateVariant
		}
		return fmt.Errorf("db insert variant: %w", err)
	}
	// A variant is born with its stock row: a purchasable thing with no stock record is not
	// a state anything downstream has to handle.
	const stockQ = `INSERT INTO stock (variant_id, quantity, reserved, sold)
	                VALUES (@variant_id, @quantity, @reserved, @sold)`
	args := pgx.NamedArgs{
		"variant_id": v.ID, "quantity": v.Stock.Quantity,
		"reserved": v.Stock.Reserved, "sold": v.Stock.Sold,
	}
	if _, err := tx.Exec(ctx, stockQ, args); err != nil {
		return fmt.Errorf("db insert stock: %w", err)
	}
	return nil
}

// updateVariant writes the variant's own columns and its quantity. reserved and sold are
// never written here — they move by the guarded statements in stock.go, and a seller edit
// must not be able to reach them.
func updateVariant(ctx context.Context, tx pgx.Tx, v *domain.Variant) error {
	const q = `UPDATE variant
	           SET price = @price, attributes = @attributes, package_details = @package_details,
	               attachments = @attachments, is_featured = @is_featured
	           WHERE id = @id`
	if _, err := tx.Exec(ctx, q, variantArgs(v)); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrDuplicateVariant
		}
		return fmt.Errorf("db update variant: %w", err)
	}
	const stockQ = `UPDATE stock SET quantity = @quantity
	                WHERE variant_id = @variant_id AND @quantity >= reserved + sold`
	args := pgx.NamedArgs{"variant_id": v.ID, "quantity": v.Stock.Quantity}
	tag, err := tx.Exec(ctx, stockQ, args)
	if err != nil {
		return fmt.Errorf("db update stock quantity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrQuantityBelowCommitted
	}
	return nil
}

func variantArgs(v *domain.Variant) pgx.NamedArgs {
	return pgx.NamedArgs{
		"id":              v.ID,
		"listing_id":      v.ListingID,
		"price":           v.Price,
		"attributes":      jsonObject(v.Attributes),
		"package_details": jsonObject(v.PackageDetails),
		"attachments":     int64Array(v.Attachments),
		"is_featured":     v.IsFeatured,
	}
}

// saveListingTags is delete-by-negation: the slice is the whole set, so no removal list has
// to be kept and forgetting to record one is not a failure mode that exists.
func saveListingTags(ctx context.Context, tx pgx.Tx, l *domain.Listing) error {
	// COALESCE, because `tag <> ALL(NULL)` is NULL rather than true: a listing whose tags
	// were all removed would otherwise keep every join row.
	const del = `DELETE FROM listing_tag
	             WHERE listing_id = @listing_id AND tag <> ALL(COALESCE(@keep::varchar[], '{}'))`
	if _, err := tx.Exec(ctx, del, pgx.NamedArgs{"listing_id": l.ID, "keep": l.Tags}); err != nil {
		return fmt.Errorf("db delete listing tags: %w", err)
	}
	const ins = `INSERT INTO listing_tag (listing_id, tag) VALUES (@listing_id, @tag)
	             ON CONFLICT (listing_id, tag) DO NOTHING`
	for _, tag := range l.Tags {
		args := pgx.NamedArgs{"listing_id": l.ID, "tag": tag}
		if _, err := tx.Exec(ctx, ins, args); err != nil {
			if isRestrictViolation(err) {
				return domain.ErrTagNotFound
			}
			return fmt.Errorf("db insert listing tag: %w", err)
		}
	}
	return nil
}

// saveListingEvents writes the trail in the same transaction as the change it describes: a
// write that landed always has one, and the diff comes from the decision rather than from a
// reconstruction after the fact.
func saveListingEvents(ctx context.Context, tx pgx.Tx, l *domain.Listing, actor int64) error {
	events := l.Events()
	if len(events) == 0 {
		return nil
	}
	// Zero means no account is responsible, which the column spells NULL.
	var changedBy *int64
	if actor != 0 {
		changedBy = &actor
	}
	snapshot := l.Snapshot()
	for _, e := range events {
		entry := port.AuditEntry{
			Table:      "listing",
			RecordID:   l.ID,
			ChangeType: "update",
			Code:       string(e.Code),
			ChangedBy:  changedBy,
			Diff:       e.Payload,
			Snapshot:   snapshot,
		}
		if err := insertAuditLog(ctx, tx, entry); err != nil {
			return err
		}
	}
	return nil
}

// IsFavorited answers false for an anonymous viewer without touching the database: a zero
// account id is not a row anybody has.
func (r *Repo) IsFavorited(ctx context.Context, accountID, listingID int64) (bool, error) {
	if accountID == 0 {
		return false, nil
	}
	const q = `SELECT EXISTS (
	               SELECT 1 FROM favorite WHERE account_id = @account_id AND listing_id = @listing_id
	           )`
	var ok bool
	args := pgx.NamedArgs{"account_id": accountID, "listing_id": listingID}
	if err := r.pool.QueryRow(ctx, q, args).Scan(&ok); err != nil {
		return false, fmt.Errorf("db query favorited: %w", err)
	}
	return ok, nil
}

func (r *Repo) CountFavorites(ctx context.Context, listingID int64) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM favorite WHERE listing_id = @listing_id`,
		pgx.NamedArgs{"listing_id": listingID}).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("db count favorites: %w", err)
	}
	return n, nil
}
