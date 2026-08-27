package catalog

import (
	"context"
	"fmt"
	"maps"

	catalogapi "shopnexus/internal/module/catalog/api"
	"shopnexus/internal/module/catalog/domain"
	"shopnexus/internal/shared/id"
)

// The three answers to "who was behind this change". A moderator is `staff` to everybody;
// only staff also get the account behind the word.
const (
	actorSeller = "seller"
	actorStaff  = "staff"
	actorSystem = "system"
)

// ListListingHistory answers the listing's trail, newest first.
//
// Two readers, one route: the seller who owns the listing and staff. A seller reads it to
// see what they changed and what was decided about it; a moderator reads it to see how a
// listing got where it is. They are not answered the same rows — see redact.
//
// A deleted listing has no history to answer. GetListing serves one — a cart line has to render
// it — so the refusal is stated here rather than inherited from the loader.
func (s *Service) ListListingHistory(ctx context.Context, req catalogapi.ListListingHistoryRequest) (catalogapi.ListingHistoryPage, error) {
	l, err := s.repo.GetListing(ctx, req.ID.Int64())
	if err != nil {
		return catalogapi.ListingHistoryPage{}, fmt.Errorf("get listing: %w", err)
	}
	if l.DeletedAt != nil {
		return catalogapi.ListingHistoryPage{}, domain.ErrListingNotFound
	}
	staff := false
	if l.SellerID != req.ActorID.Int64() {
		if staff, err = s.isStaff(ctx, req.ActorID); err != nil {
			return catalogapi.ListingHistoryPage{}, err
		}
		// Somebody else's listing is not theirs to know about, which is the same answer the
		// seller-scoped loads give.
		if !staff {
			return catalogapi.ListingHistoryPage{}, domain.ErrListingNotFound
		}
	}

	records, total, err := s.repo.ListListingHistory(ctx, l.ID, offsetOf(req.Page, req.Limit), req.Limit)
	if err != nil {
		return catalogapi.ListingHistoryPage{}, fmt.Errorf("list listing history: %w", err)
	}

	// One resolve for the page rather than one per entry, and only for the accounts this
	// reader is allowed to see named.
	named := make([]int64, 0, len(records))
	for _, rec := range records {
		if kind := actorKindOf(rec.ChangedBy, l.SellerID); kind == actorSeller || (kind == actorStaff && staff) {
			named = append(named, *rec.ChangedBy)
		}
	}
	accounts := s.sellers(ctx, named)

	entries := make([]catalogapi.ListingHistoryEntry, 0, len(records))
	for _, rec := range records {
		kind := actorKindOf(rec.ChangedBy, l.SellerID)
		entry := catalogapi.ListingHistoryEntry{
			Version:    rec.Version,
			Code:       rec.Code,
			ChangeType: rec.ChangeType,
			ChangedAt:  rec.ChangedAt,
			ActorKind:  kind,
			Fields:     fieldsOf(rec.Diff),
			Details:    publishIDs(redact(rec.Code, rec.Diff, staff)),
		}
		if account, ok := accounts[actorIDOf(rec.ChangedBy)]; ok && (kind == actorSeller || staff) {
			entry.Actor = &account
		}
		entries = append(entries, entry)
	}

	return catalogapi.ListingHistoryPage{
		Data: entries,
		Meta: catalogapi.PageInfo{Page: req.Page, Limit: req.Limit, TotalCount: &total},
	}, nil
}

// publishIDs converts the keys of a payload that are database ids into the opaque form
// every other id on the wire has. The trail stores raw int64s — it is a database table, and
// the encoding is the api layer's job — so this is where that conversion happens for it.
func publishIDs(details map[string]any) map[string]any {
	raw, ok := details["variant_id"]
	if !ok {
		return details
	}
	// JSON has one number type, so what a jsonb column gives back is a float64 whatever was
	// written. A value that is not a number at all was not an id and is dropped rather than
	// published as something a client would try to parse.
	n, ok := raw.(float64)
	if !ok {
		delete(details, "variant_id")
		return details
	}
	details["variant_id"] = id.Of[id.Variant](int64(n))
	return details
}

func actorKindOf(changedBy *int64, sellerID int64) string {
	switch {
	case changedBy == nil:
		return actorSystem
	case *changedBy == sellerID:
		return actorSeller
	default:
		return actorStaff
	}
}

func actorIDOf(changedBy *int64) int64 {
	if changedBy == nil {
		return 0
	}
	return *changedBy
}

// fieldsOf lifts the field list out of the payload that carries one. It arrives as JSON, so
// the slice is []any of strings; anything else in there is not a field name and is dropped
// rather than rendered.
func fieldsOf(diff map[string]any) []string {
	raw, _ := diff["fields"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if name, ok := v.(string); ok {
			out = append(out, name)
		}
	}
	return out
}

// redact is what makes one trail readable by two audiences.
//
// A takedown's `reason` is the moderator's full words and `notify_seller` was their choice
// about sending them: the row already carries whatever the seller may read (nil when they
// chose not to tell them), so answering the raw payload here would hand over exactly the
// sentence that choice withheld. An approval's `note` is the same — moderators write it for
// each other. Everything else is the seller's own history and reads back to them unchanged.
func redact(code string, diff map[string]any, staff bool) map[string]any {
	out := make(map[string]any, len(diff))
	maps.Copy(out, diff)
	// Fields have their own place on the entry.
	delete(out, "fields")
	if staff {
		return out
	}
	switch domain.EventCode(code) {
	case domain.TakenDown.Code:
		if notify, _ := out["notify_seller"].(bool); !notify {
			delete(out, "reason")
		}
		delete(out, "notify_seller")
	case domain.Approved.Code:
		delete(out, "note")
	}
	return out
}
