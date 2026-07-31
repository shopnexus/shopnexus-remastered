// Package order implements orderapi.Service — the cart, the purchase session, the
// negotiation, the order, its shipment and its refunds.
//
// The money creates the order: finance's session completing is what writes it, so nothing
// here waits on a seller pressing a button. The timed transitions — a draft expiring, an
// escrow window closing, a refund deadline passing — are idempotent methods a durable
// workflow drives, which is why none of them keeps state of its own.
package order

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-playground/validator/v10"

	"shopnexus/internal/infra/eventbus"
	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/common"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	"shopnexus/internal/module/order/domain"
	"shopnexus/internal/module/order/port"
	"shopnexus/internal/shared/id"
)

// The two windows this module owns. A draft is short because it freezes a price; a
// negotiation is longer because it waits on a person.
const (
	draftWindow = 30 * time.Minute
	offerWindow = 48 * time.Hour
)

type Service struct {
	repo port.Repository
	// accounts answers the caller's role and their delivery contacts; catalog answers what
	// a listing costs and moves its stock; finance owns every money primitive; chat carries
	// the negotiation's cards.
	accounts accountapi.Service
	catalog  catalogapi.Service
	finance  financeapi.Service
	chat     chatapi.Service
	// options is the carrier registry — this module's own `option` rows, so a carrier
	// nobody enabled cannot be chosen.
	options port.Options
	// workflows holds the timers: the durable runtime when there is one, nothing when there
	// is not. Best-effort at every call site — the row is already committed.
	workflows port.Workflows
	bus       eventbus.Client
	v         *validator.Validate
	log       *slog.Logger
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	catalog catalogapi.Service,
	finance financeapi.Service,
	chat chatapi.Service,
	options port.Options,
	workflows port.Workflows,
	bus eventbus.Client,
	v *validator.Validate,
	log *slog.Logger,
) *Service {
	return &Service{
		repo: repo, accounts: accounts, catalog: catalog, finance: finance, chat: chat,
		options: options, workflows: workflows, bus: bus, v: v, log: log,
	}
}

var _ orderapi.Service = (*Service)(nil)

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

// transportOption resolves a carrier from this module's registry. A slug nobody enabled is
// refused here rather than handed to a courier that has never heard of it.
func (s *Service) transportOption(ctx context.Context, slug string) error {
	options, err := s.options.ListEnabled(ctx, common.OptionTypeTransport)
	if err != nil {
		return fmt.Errorf("list transport options: %w", err)
	}
	for _, o := range options {
		if o.ID == slug {
			return nil
		}
	}
	return domain.ErrCarrierUnknown
}

// contactSnapshot copies a saved contact into the row. A pointer would not do: the
// administrative codes are what a carrier is called with, and the saved contact may have
// changed by the time a parcel moves.
func (s *Service) contactSnapshot(ctx context.Context, actorID id.ID[id.Account], contactID id.ID[id.Contact]) (domain.AddressSnapshot, error) {
	contact, err := s.accounts.GetContact(ctx, accountapi.GetContactRequest{
		ActorID: actorID, ID: contactID,
	})
	if err != nil {
		return domain.AddressSnapshot{}, fmt.Errorf("read contact: %w", err)
	}
	return snapshotOf(contact), nil
}

// pickupSnapshot is the seller's collection point, read while they are not present: the
// money landing is what creates the shipment, and nobody asks them then.
func (s *Service) pickupSnapshot(ctx context.Context, sellerID int64) (domain.AddressSnapshot, error) {
	contact, err := s.accounts.GetPickupContact(ctx, accountapi.GetPickupContactRequest{
		AccountID: id.Of[id.Account](sellerID),
	})
	if err != nil {
		return domain.AddressSnapshot{}, fmt.Errorf("read pickup contact: %w", err)
	}
	return snapshotOf(contact), nil
}

func snapshotOf(contact accountapi.Contact) domain.AddressSnapshot {
	return domain.AddressSnapshot{
		FullName:      contact.FullName,
		Phone:         contact.Phone,
		Country:       contact.Country,
		ProvinceCode:  contact.ProvinceCode,
		DistrictCode:  contact.DistrictCode,
		WardCode:      contact.WardCode,
		AddressDetail: contact.AddressDetail,
	}
}

// requireResources refuses evidence that names no confirmed upload of this module's: a
// dispute decided on a photo that does not render is a decision nobody can review.
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

func (s *Service) summary(ctx context.Context, accountID int64) (accountapi.AccountSummary, error) {
	account, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
		ID: id.Of[id.Account](accountID),
	})
	if err != nil {
		return accountapi.AccountSummary{}, fmt.Errorf("read account: %w", err)
	}
	return accountapi.AccountSummary{ID: account.ID, Name: account.Name, Avatar: account.Avatar}, nil
}

func resourceKeys(ids []id.ID[id.Resource]) []int64 {
	out := make([]int64, 0, len(ids))
	for _, rid := range ids {
		out = append(out, rid.Int64())
	}
	return out
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

// cursorFilter reads one more row than asked, so "is there another page" is answered
// without a count.
func cursorFilter(cursor string, limit int) (port.CursorFilter, error) {
	before, err := parseCursor(cursor)
	if err != nil {
		return port.CursorFilter{}, err
	}
	return port.CursorFilter{Before: before, Limit: limit + 1}, nil
}

// timer is every call into the durable runtime: the row is already committed, so a runtime
// that is unreachable is a slower clock rather than a failed request — the sweep still moves
// the state. Logged at warn because a deployment where every one of these fails has lost its
// promptness, and nothing else would say so.
func (s *Service) timer(what string, err error) {
	if err != nil {
		s.log.Warn("durable timer not set", "what", what, "err", err)
	}
}
