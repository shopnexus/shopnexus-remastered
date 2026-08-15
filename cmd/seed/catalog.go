package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopnexus/internal/module/common/dbx"
)

// catalogIDs is what every later step needs back: the ids Postgres assigned, in plan order.
type catalogIDs struct {
	listings []int64
	variants [][]int64
	// coverResource is the first photo of each listing, reused as the thumbnail a chat
	// reference or a review would otherwise have to resolve on its own.
	coverResource []int64
}

// writeCatalog lands the whole catalogue in one transaction: the category tree, one resource
// row per generated photo, and every listing with its variants, stock and tags.
//
// Everything is written with "embedding_stale_at" set, which is how the sync cron is told there
// is work. Nothing here writes a vector — that is the embedding job's, and it needs a model
// this command does not have.
func writeCatalog(ctx context.Context, pool *pgxpool.Pool, p *plan, parties map[string]party) (catalogIDs, error) {
	var out catalogIDs
	now := p.now

	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		categoryID, err := writeCategories(ctx, tx, p.categories, now)
		if err != nil {
			return err
		}
		resourceID, err := writeListingPhotos(ctx, tx, p.images, now)
		if err != nil {
			return err
		}

		out.listings = make([]int64, len(p.listings))
		out.variants = make([][]int64, len(p.listings))
		out.coverResource = make([]int64, len(p.listings))
		stock := &pgx.Batch{}
		listingTags := &pgx.Batch{}
		tagSet := map[string]bool{}

		for i, l := range p.listings {
			seller, ok := parties[l.seller]
			if !ok {
				return fmt.Errorf("listing %q: no such seller %q", l.slug, l.seller)
			}
			attachments := make([]int64, 0, len(l.images))
			for _, at := range l.images {
				attachments = append(attachments, resourceID[p.images[at].key])
			}
			if len(attachments) > 0 {
				out.coverResource[i] = attachments[0]
			}

			const insertListing = `
				INSERT INTO listing (slug, account_id, category_id, status, name, description,
				                     specifications, attachments, price_mode, condition, currency,
				                     cached_rating, cached_review_count, cached_sold,
				                     province_code, province_name, ward_code, ward_name,
				                     taken_down_at, takedown_reason,
				                     created_at, embedding_stale_at)
				VALUES (@slug, @account_id, @category_id, @status, @name, @description,
				        @specifications, @attachments, @price_mode, @condition, @currency,
				        @cached_rating, @cached_review_count, @cached_sold,
				        @province_code, @province_name, @ward_code, @ward_name,
				        @taken_down_at, @takedown_reason,
				        @created_at, @now)
				RETURNING id`
			var takenDownAt any
			var takedownReason any
			if l.takedown != "" {
				takenDownAt = l.createdAt.Add(48 * time.Hour)
				takedownReason = l.takedown
			}
			args := pgx.NamedArgs{
				"slug":       l.slug,
				"account_id": seller.id,
				// Where the goods are, copied from the seller's pickup contact exactly as
				// PublishListing does — a live listing always has one, and the browse feed's
				// area filter sees nothing without it.
				"province_code":       seller.area.provinceCode,
				"province_name":       seller.area.provinceName,
				"ward_code":           seller.area.wardCode,
				"ward_name":           seller.area.wardName,
				"category_id":         categoryID[l.category],
				"status":              l.status,
				"name":                l.name,
				"description":         l.description,
				"specifications":      dbx.JSONObject(l.specs),
				"attachments":         dbx.Int64Array(attachments),
				"price_mode":          l.priceMode,
				"condition":           l.condition,
				"currency":            currency,
				"cached_rating":       l.cachedRating,
				"cached_review_count": l.cachedReviewCount,
				"cached_sold":         l.cachedSold,
				"taken_down_at":       takenDownAt,
				"takedown_reason":     takedownReason,
				"created_at":          l.createdAt,
				"now":                 now,
			}
			if err := tx.QueryRow(ctx, insertListing, args).Scan(&out.listings[i]); err != nil {
				return fmt.Errorf("insert listing %q: %w", l.slug, err)
			}

			out.variants[i] = make([]int64, len(l.variants))
			for j, v := range l.variants {
				const insertVariant = `
					INSERT INTO variant (listing_id, price, attributes, package_details,
					                     is_featured, created_at)
					VALUES (@listing_id, @price, @attributes, @package_details,
					        @is_featured, @created_at)
					RETURNING id`
				args := pgx.NamedArgs{
					"listing_id":      out.listings[i],
					"price":           v.price,
					"attributes":      dbx.JSONObject(v.attributes),
					"package_details": dbx.JSONObject(v.pkg),
					"is_featured":     v.featured,
					"created_at":      l.createdAt,
				}
				if err := tx.QueryRow(ctx, insertVariant, args).Scan(&out.variants[i][j]); err != nil {
					return fmt.Errorf("insert variant of %q: %w", l.slug, err)
				}
				stock.Queue(
					`INSERT INTO stock (variant_id, quantity, sold, created_at)
					 VALUES (@variant_id, @quantity, @sold, @created_at)`,
					pgx.NamedArgs{
						"variant_id": out.variants[i][j],
						"quantity":   v.quantity,
						"sold":       v.sold,
						"created_at": l.createdAt,
					})
			}

			for _, tag := range l.tags {
				tagSet[tag] = true
				listingTags.Queue(
					`INSERT INTO listing_tag (listing_id, tag) VALUES (@listing_id, @tag)`,
					pgx.NamedArgs{"listing_id": out.listings[i], "tag": tag})
			}
		}

		if err := tx.SendBatch(ctx, stock).Close(); err != nil {
			return fmt.Errorf("insert stock: %w", err)
		}
		// Tags before the join rows: "listing_tag"."tag" has a foreign key onto them.
		if err := writeTags(ctx, tx, tagSet, now); err != nil {
			return err
		}
		if err := tx.SendBatch(ctx, listingTags).Close(); err != nil {
			return fmt.Errorf("insert listing tags: %w", err)
		}
		return nil
	})
	if err != nil {
		return catalogIDs{}, err
	}
	return out, nil
}

// writeCategories is the one place this command touches something arguably not its own.
//
// A category tree is bootstrap: a listing cannot be written without one ("listing"."category_id"
// is NOT NULL), so a deployment with an empty "category" table cannot take a listing at all —
// which is the definition of data the system does not run without. It lives here for historical
// reasons and should move into a catalog migration; until it does, this is written to be safe
// to run over an existing tree: ON CONFLICT on the name, never an UPDATE, and the wipe never
// deletes a category. See the note in the package comment.
func writeCategories(ctx context.Context, tx pgx.Tx, cats []category, now time.Time) (map[string]int64, error) {
	out := make(map[string]int64, len(cats))
	for _, c := range cats {
		const q = `
			INSERT INTO category (name, description, embedding_stale_at)
			VALUES (@name, @description, @now)
			ON CONFLICT (name) DO NOTHING
			RETURNING id`
		var id int64
		args := pgx.NamedArgs{"name": c.Name, "description": c.Description, "now": now}
		err := tx.QueryRow(ctx, q, args).Scan(&id)
		if err == nil {
			out[c.Name] = id
			continue
		}
		if !dbx.IsNoRows(err) {
			return nil, fmt.Errorf("insert category %q: %w", c.Name, err)
		}
		// Already there, and left exactly as it was.
		if err := tx.QueryRow(ctx,
			`SELECT id FROM category WHERE name = @name`,
			pgx.NamedArgs{"name": c.Name}).Scan(&id); err != nil {
			return nil, fmt.Errorf("read existing category %q: %w", c.Name, err)
		}
		out[c.Name] = id
	}
	return out, nil
}

// writeListingPhotos records each generated photo as a resource of the "local" provider — the
// store the gateway serves objects from, so the bytes written by writePhotos are the bytes a
// browser gets back through a signed URL.
//
// "uploaded_by_id" is NULL because nobody uploaded anything, which is exactly what the column's
// NULL means. "completed_at" is set, because the object really is there: a resource without it
// is a reservation, and dbx.Find drops those, so the gallery would silently come back empty.
func writeListingPhotos(ctx context.Context, tx pgx.Tx, photos []photo, now time.Time) (map[string]int64, error) {
	out := make(map[string]int64, len(photos))
	if len(photos) == 0 {
		return out, nil
	}
	keys := make([]string, len(photos))
	sizes := make([]int64, len(photos))
	mimes := make([]string, len(photos))
	for i, p := range photos {
		keys[i] = p.key
		sizes[i] = p.size
		mimes[i] = p.mime
	}
	// The mime is per row, not per batch: a gallery mixes committed JPEG photographs with
	// drawn PNG placeholders, and a row that misdescribes its bytes is served with the wrong
	// content type.
	const q = `
		INSERT INTO resource (uploaded_by_id, provider, object_key, mime, size,
		                      metadata, created_at, completed_at)
		SELECT NULL, 'local', r.key, r.mime, r.size, '{}', @now, @now
		FROM unnest(@keys::text[], @sizes::bigint[], @mimes::text[]) AS r(key, size, mime)
		RETURNING id, object_key`
	rows, err := tx.Query(ctx, q, pgx.NamedArgs{
		"keys": keys, "sizes": sizes, "mimes": mimes, "now": now,
	})
	if err != nil {
		return nil, fmt.Errorf("insert listing photos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan photo resource: %w", err)
		}
		out[key] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insert listing photos: %w", err)
	}
	return out, nil
}

func writeTags(ctx context.Context, tx pgx.Tx, tags map[string]bool, now time.Time) error {
	ids := make([]string, 0, len(tags))
	for tag := range tags {
		ids = append(ids, tag)
	}
	const q = `
		INSERT INTO tag (id, description, embedding_stale_at)
		SELECT t.id, t.id, @now FROM unnest(@ids::text[]) AS t(id)
		ON CONFLICT (id) DO NOTHING`
	if _, err := tx.Exec(ctx, q, pgx.NamedArgs{"ids": ids, "now": now}); err != nil {
		return fmt.Errorf("insert tags: %w", err)
	}
	return nil
}

// evidenceResource records one photo that already exists in the object store, into whichever
// schema the caller's transaction is pointed at. A receipt lives in order's schema and a
// review's photos in trust's — the resource table is shared DDL applied into every module, so
// an id from one is meaningless in another.
func evidenceResource(ctx context.Context, tx pgx.Tx, key string, size int64, now time.Time) (int64, error) {
	const q = `
		INSERT INTO resource (uploaded_by_id, provider, object_key, mime, size,
		                      metadata, created_at, completed_at)
		VALUES (NULL, 'local', @object_key, @mime, @size, '{}', @now, @now)
		ON CONFLICT (provider, object_key) DO UPDATE SET size = EXCLUDED.size
		RETURNING id`
	var id int64
	args := pgx.NamedArgs{"object_key": key, "mime": drawnMime, "size": size, "now": now}
	if err := tx.QueryRow(ctx, q, args).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert evidence resource %q: %w", key, err)
	}
	return id, nil
}
