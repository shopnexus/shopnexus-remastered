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
	"strings"
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
	// uploads is this module's own resource table plus the object store. A review photo
	// belongs to the module that took the upload, and resolving one through here is what
	// puts a live link on it rather than an id nothing can render.
	uploads common.Uploads
	v       *validator.Validate
	log     *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	catalog catalogapi.Service,
	orders orderapi.Service,
	chat chatapi.Service,
	uploads common.Uploads,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{repo: repo, accounts: accounts, catalog: catalog, orders: orders,
		chat: chat, uploads: uploads, v: v, log: log}
}

// CreateUpload reserves a row and a signed slot for a review photo. The client PUTs the
// bytes at the store and confirms; until then the resource resolves to nothing, so a
// half-finished upload cannot be attached to a review.
func (s *Service) CreateUpload(ctx context.Context, req trustapi.CreateUploadRequest) (trustapi.UploadSlot, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.UploadSlot{}, err
	}
	slot, err := s.uploads.Presign(ctx, req.ActorID.Int64(), "review", common.UploadRequest{
		Filename: req.Filename, Mime: req.Mime, Size: req.Size,
	})
	if err != nil {
		return trustapi.UploadSlot{}, err
	}
	return trustapi.UploadSlot{
		ResourceID: id.Of[id.Resource](slot.ResourceID),
		URL:        slot.URL,
		Headers:    slot.Headers,
		ExpiresAt:  slot.ExpiresAt,
	}, nil
}

// ConfirmUpload makes the photo real, with the size the store reports rather than the one
// the client declared. Scoped to the uploader: a resource id is guessable, and confirming
// somebody else's slot would be claiming their upload.
func (s *Service) ConfirmUpload(ctx context.Context, req trustapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	if err := s.v.Struct(req); err != nil {
		return common.ResourceDTO{}, err
	}
	res, err := s.uploads.Confirm(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return common.ResourceDTO{}, err
	}
	return res.ToDTO(), nil
}

var _ trustapi.Service = (*Service)(nil)

// requireModerator asks the account module for the caller's role: it is a row in that
// module's table. An admin passes every moderator check.
func (s *Service) requireModerator(ctx context.Context, actorID id.ID[id.Account]) error {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return fmt.Errorf("read caller role: %w", err)
	}
	if me.Role != accountapi.RoleModerator && me.Role != accountapi.RoleAdmin {
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
	return s.uploads.Resolve(ctx, keys)
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

// The cursor is the sort key the page ended at *and* that row's id — opaque to a client, and
// stable under a list that keeps moving. Both halves are needed: CURRENT_TIMESTAMP is
// transaction-scoped, so rows written together share a timestamp exactly, and a key on its own
// would skip whichever of them the page did not reach.
func formatCursor(key, rowID int64) string {
	return strconv.FormatInt(key, 10) + "." + strconv.FormatInt(rowID, 10)
}

func parseCursor(cursor string) (key, rowID int64, err error) {
	if cursor == "" {
		return 0, 0, nil
	}
	dot := strings.IndexByte(cursor, '.')
	if dot < 0 {
		return 0, 0, domain.ErrCursorInvalid
	}
	key, err = strconv.ParseInt(cursor[:dot], 10, 64)
	if err != nil {
		return 0, 0, domain.ErrCursorInvalid
	}
	rowID, err = strconv.ParseInt(cursor[dot+1:], 10, 64)
	if err != nil || rowID <= 0 {
		return 0, 0, domain.ErrCursorInvalid
	}
	return key, rowID, nil
}

// timeCursor and countCursor are the two keys the lists here order by: created_at for nearly
// all of them, the helpfulness tally for a review page sorted by it. Each reads one more row
// than asked, so "is there another page" is answered without a count.
func timeCursor(cursor string, limit int) (port.CursorFilter, error) {
	key, rowID, err := parseCursor(cursor)
	if err != nil {
		return port.CursorFilter{}, err
	}
	f := port.CursorFilter{BeforeID: rowID, Limit: limit + 1}
	if rowID != 0 {
		f.Before = time.Unix(0, key)
	}
	return f, nil
}

func countCursor(cursor string, limit int) (port.CursorFilter, error) {
	key, rowID, err := parseCursor(cursor)
	if err != nil {
		return port.CursorFilter{}, err
	}
	return port.CursorFilter{BeforeCount: key, BeforeID: rowID, Limit: limit + 1}, nil
}

// paginate trims the extra row and reports the cursor the next page starts from. key answers
// the pair the ORDER BY sorted on, so a caller cannot hand back a cursor over a column the
// query never ordered by.
func paginate[T any](rows []T, limit int, key func(T) (int64, int64)) ([]T, trustapi.CursorInfo) {
	if len(rows) <= limit {
		return rows, trustapi.CursorInfo{}
	}
	rows = rows[:limit]
	last, rowID := key(rows[len(rows)-1])
	return rows, trustapi.CursorInfo{
		NextCursor: formatCursor(last, rowID),
		HasMore:    true,
	}
}
