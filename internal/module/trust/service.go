// Package trust implements trustapi.Service — order feedback, product reviews with their
// replies and votes, per-account reputation, and abuse reports.
//
// Reputation is never written through the API: it is folded in by the same transaction that
// publishes a rating or writes a review, so a visible rating is always a counted one.
package trust

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-playground/validator/v10"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/common"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/id"
)

// repliesOnAPage is how much of a thread a listing's review page carries. Replies are
// unlimited, so a page of twenty reviews cannot hold all of them — reply_count says how
// many there are and the single-review read returns the rest.
const repliesOnAPage = 3

type Service struct {
	repo port.Repository
	// accounts answers the caller's role and the names beside a review or a report; catalog
	// answers who owns a listing and caches its rating; order says whether a sale is
	// finished and what it covered; chat hands a moderator the reported message.
	accounts accountapi.Service
	catalog  catalogapi.Service
	orders   orderapi.Service
	chat     chatapi.Service
	v        *validator.Validate
	log      *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	catalog catalogapi.Service,
	orders orderapi.Service,
	chat chatapi.Service,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, accounts: accounts, catalog: catalog, orders: orders,
		chat: chat, v: v, log: log}
}

var _ trustapi.Service = (*Service)(nil)

// requireModerator asks the account module for the caller's role: it is a row in that
// module's table. An admin passes every moderator check.
func (s *Service) requireModerator(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != "moderator" && me.Role != "admin" {
		return domain.ErrModeratorRequired
	}
	return nil
}

// isModerator is the same question where the answer is a permission rather than a refusal —
// a moderator may delete somebody else's review.
func (s *Service) isModerator(ctx context.Context, actorID id.ID[id.Account]) bool {
	if actorID == 0 {
		return false
	}
	return s.requireModerator(ctx, actorID) == nil
}

func (s *Service) summary(ctx context.Context, accountID int64) (accountapi.AccountSummary, error) {
	account, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
		ID: id.Of[id.Account](accountID),
	})
	if err != nil {
		return accountapi.AccountSummary{}, fmt.Errorf("read account: %w", err)
	}
	return accountapi.AccountSummary{ID: account.ID, Name: account.Name, Avatar: account.Avatar}, nil
}

// summaries resolves a page's authors once each: a page of twenty reviews by two people is
// two lookups, not twenty.
func (s *Service) summaries(ctx context.Context, accountIDs []int64) map[int64]accountapi.AccountSummary {
	out := make(map[int64]accountapi.AccountSummary, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, done := out[accountID]; done {
			continue
		}
		found, err := s.summary(ctx, accountID)
		if err != nil {
			// A deleted account must not hide the content it wrote: the row is what the
			// page is about, and a name is decoration on it.
			s.log.Debug("resolve author failed", "account_id", accountID, "err", err)
			out[accountID] = accountapi.AccountSummary{ID: id.Of[id.Account](accountID)}
			continue
		}
		out[accountID] = found
	}
	return out
}

// requireResources refuses attachments that name no confirmed upload of this module's: a
// review photo that does not render is worse than a review without one.
func (s *Service) requireResources(ctx context.Context, keys []int64) error {
	if len(keys) == 0 {
		return nil
	}
	found, err := s.resources(ctx, keys)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, ok := found[key]; !ok {
			return domain.ErrAttachmentNotFound
		}
	}
	return nil
}

func (s *Service) resources(ctx context.Context, keys []int64) (map[int64]common.ResourceDTO, error) {
	out := make(map[int64]common.ResourceDTO, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	found, err := s.repo.FindResources(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("find resources: %w", err)
	}
	for _, res := range found {
		out[res.ID] = res.ToDTO()
	}
	return out, nil
}

func pick(found map[int64]common.ResourceDTO, keys []int64) []common.ResourceDTO {
	out := make([]common.ResourceDTO, 0, len(keys))
	for _, key := range keys {
		if res, ok := found[key]; ok {
			out = append(out, res)
		}
	}
	return out
}

func rawIDs[K id.Kind](ids []id.ID[K]) []int64 {
	out := make([]int64, 0, len(ids))
	for _, one := range ids {
		out = append(out, one.Int64())
	}
	return out
}

// The cursor is the timestamp a page ended at, in nanoseconds — opaque to a client, and
// stable under a list that keeps moving.
func formatCursor(at time.Time) string { return strconv.FormatInt(at.UnixNano(), 10) }

func parseCursor(cursor string) (time.Time, error) {
	if cursor == "" {
		return time.Time{}, nil
	}
	nanos, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil {
		return time.Time{}, domain.ErrCursorInvalid
	}
	return time.Unix(0, nanos), nil
}

// cursorFilter reads one more row than asked, so "is there another page" is answered without
// a count.
func cursorFilter(cursor string, limit int) (port.CursorFilter, error) {
	before, err := parseCursor(cursor)
	if err != nil {
		return port.CursorFilter{}, err
	}
	return port.CursorFilter{Before: before, Limit: limit + 1}, nil
}

// paginate trims the extra row and reports the cursor the next page starts from.
func paginate[T any](rows []T, limit int, at func(T) time.Time) ([]T, trustapi.CursorInfo) {
	if len(rows) <= limit {
		return rows, trustapi.CursorInfo{}
	}
	rows = rows[:limit]
	return rows, trustapi.CursorInfo{
		NextCursor: formatCursor(at(rows[len(rows)-1])),
		HasMore:    true,
	}
}
