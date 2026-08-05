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
	"slices"
	"strings"
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
	"shopnexus/internal/provider/transport"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/realtime"
)

// Every wait this module makes. A draft is short because it freezes a price; a standing proposal
// is longer because it waits on a person.
const (
	draftWindow = 30 * time.Minute
	// offerWindow is how long a standing proposal waits for the other side. Short by design: a
	// price left open for days is a price the market has moved past, and either party can always
	// open a new negotiation.
	offerWindow = 12 * time.Hour
	// acceptedWindow is how long the buyer has to turn agreed terms into an order. The same
	// half-hour a draft gets, for the same reason: both are a frozen price, and stock nobody has
	// reserved yet must not be promised at yesterday's number.
	acceptedWindow = 30 * time.Minute
	// checkoutWindow is how long an unpaid checkout holds its reservation. Read by the sweep as
	// well as by the timer, which is why it lives with the other windows rather than beside one
	// of its two callers.
	checkoutWindow = 15 * time.Minute
	// bookingGrace is how long a shipment is left alone before the retry pass tries the carrier
	// again: long enough that a booking still in flight is never raced by the sweep.
	bookingGrace = 2 * time.Minute
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
	// uploads is this module's own resource table plus the object store. Evidence — receipt
	// and refund photos — belongs to the module that took it, and resolving it through here is
	// what puts a live signed link on it rather than a bare id nothing can render.
	uploads common.Uploads
	// options is the carrier registry — this module's own `option` rows, so a carrier
	// nobody enabled cannot be chosen; transport is the courier those slugs price with.
	options   port.Options
	transport transport.Client
	// courierProvider is the courier this deployment configured, which decides which registry
	// rows are offered at all.
	courierProvider CourierProvider
	// workflows holds the timers: the durable runtime when there is one, nothing when there
	// is not. Best-effort at every call site — the row is already committed.
	workflows port.Workflows
	bus       eventbus.Client
	v         *validator.Validate
	log       *slog.Logger
	// fanout pushes the realtime facts in event.go to the socket a party may have open.
	// Best-effort: a write always commits whether or not anybody is listening.
	fanout realtime.Fanout
}

func NewService(
	repo port.Repository,
	accounts accountapi.Service,
	catalog catalogapi.Service,
	finance financeapi.Service,
	chat chatapi.Service,
	uploads common.Uploads,
	options port.Options,
	transport transport.Client,
	courierProvider CourierProvider,
	workflows port.Workflows,
	bus eventbus.Client,
	v *validator.Validate,
	log *slog.Logger,
	fanout realtime.Fanout,
) *Service {
	return &Service{
		repo: repo, accounts: accounts, catalog: catalog, finance: finance, chat: chat,
		uploads: uploads, options: options, transport: transport,
		courierProvider: courierProvider, workflows: workflows,
		bus: bus, v: v, log: log, fanout: fanout,
	}
}

// CourierProvider is the configured TRANSPORT_PROVIDER. Its own type, not a bare string: the fx
// graph is keyed by type, and "a string" is not a thing to inject.
type CourierProvider string

var _ orderapi.Service = (*Service)(nil)

// notify pushes a fact to one account, best-effort.
//
// A realtime failure never fails the command: the row is committed by the time this runs,
// so the alternative is answering 500 for a write that happened. The client re-reads on
// reconnect, which is what covers a dropped event.
func notify[T any](ctx context.Context, s *Service, accountID int64, e realtime.Event[T], data T) {
	if err := realtime.Notify(ctx, s.fanout, accountID, e, data); err != nil {
		s.log.Warn("realtime notify failed", "code", e.Code, "account_id", accountID, "err", err)
	}
}

// CreateUpload reserves a row and a signed slot for evidence — the unboxing photos a receipt
// confirmation or a refund carries. The client PUTs the bytes at the store and confirms; until
// then the resource resolves to nothing, so a half-finished upload cannot be named as evidence.
func (s *Service) CreateUpload(ctx context.Context, req orderapi.CreateUploadRequest) (orderapi.UploadSlot, error) {
	if err := s.v.Struct(req); err != nil {
		return orderapi.UploadSlot{}, err
	}
	slot, err := s.uploads.Presign(ctx, req.ActorID.Int64(), "evidence", common.UploadRequest{
		Filename: req.Filename, Mime: req.Mime, Size: req.Size,
	})
	if err != nil {
		return orderapi.UploadSlot{}, err
	}
	return orderapi.UploadSlot{
		ResourceID: id.Of[id.Resource](slot.ResourceID),
		URL:        slot.URL,
		Headers:    slot.Headers,
		ExpiresAt:  slot.ExpiresAt,
	}, nil
}

// ConfirmUpload makes the evidence real, with the size the store reports rather than the one
// the client declared. Scoped to the uploader: a resource id is guessable, and confirming
// somebody else's slot would be claiming their upload.
func (s *Service) ConfirmUpload(ctx context.Context, req orderapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	if err := s.v.Struct(req); err != nil {
		return common.ResourceDTO{}, err
	}
	res, err := s.uploads.Confirm(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return common.ResourceDTO{}, err
	}
	return res.ToDTO(), nil
}

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

// carriers is the registry filtered to what this deployment's courier can actually serve, best
// first: a row naming a vendor the stack cannot reach is a checkout that fails at the last step,
// and the mock's scenario rows must never appear beside a real courier's services.
func (s *Service) carriers(ctx context.Context) ([]common.Option, error) {
	options, err := s.options.ListEnabled(ctx, common.OptionTypeTransport)
	if err != nil {
		return nil, fmt.Errorf("list transport options: %w", err)
	}
	offered := make([]common.Option, 0, len(options))
	for _, o := range options {
		if o.Offered(string(s.courierProvider)) {
			offered = append(offered, o)
		}
	}
	return offered, nil
}

// transportOption resolves a carrier from this module's registry. A slug nobody enabled is
// refused here rather than handed to a courier that has never heard of it.
func (s *Service) transportOption(ctx context.Context, slug string) error {
	options, err := s.carriers(ctx)
	if err != nil {
		return err
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

// deliverySnapshot is where a quote goes when the caller named no address: their default. A
// checkout still names one — it is snapshotted onto the order, so the buyer has to have chosen
// it — but a listing page asking "what would delivery cost" should not need a form first.
func (s *Service) deliverySnapshot(ctx context.Context, actorID id.ID[id.Account],
	contactID id.ID[id.Contact]) (domain.AddressSnapshot, id.ID[id.Contact], error) {
	if contactID != 0 {
		address, err := s.contactSnapshot(ctx, actorID, contactID)
		return address, contactID, err
	}
	contact, err := s.accounts.GetDeliveryContact(ctx, accountapi.GetDeliveryContactRequest{
		ActorID: actorID,
	})
	if err != nil {
		return domain.AddressSnapshot{}, 0, fmt.Errorf("read default delivery contact: %w", err)
	}
	return snapshotOf(contact), contact.ID, nil
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
	return s.uploads.Resolve(ctx, keys)
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

// The cursor is a (timestamp, id) tuple; common owns the format, and the reason it is a tuple.
func formatCursor(at time.Time, id int64) string {
	return common.FormatCursor(at.UnixNano(), id)
}

func parseCursor(cursor string) (time.Time, int64, error) {
	nanos, id, err := common.ParseCursor(cursor)
	if err != nil || id == 0 {
		return time.Time{}, 0, err
	}
	return time.Unix(0, nanos), id, nil
}

// page cuts the extra row every list reads and turns the last one into the next cursor. Six routes
// had this written out; the accessor is what keeps it honest for a list keyed by something other
// than `created_at`.
func page[T any](rows []T, limit int, key func(T) (time.Time, int64)) ([]T, orderapi.CursorInfo) {
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	meta := orderapi.CursorInfo{HasMore: hasMore}
	if hasMore && len(rows) > 0 {
		meta.NextCursor = formatCursor(key(rows[len(rows)-1]))
	}
	return rows, meta
}

// cursorFilter reads one more row than asked, so "is there another page" is answered
// without a count.
func cursorFilter(cursor string, limit int) (port.CursorFilter, error) {
	before, beforeID, err := parseCursor(cursor)
	if err != nil {
		return port.CursorFilter{}, err
	}
	return port.CursorFilter{Before: before, BeforeID: beforeID, Limit: limit + 1}, nil
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

// quoteShipping prices delivery for one checkout. The buyer pays it on every sale — a fixed-price
// purchase and a negotiated one alike — so a quote that is never asked for is a courier bill the
// platform silently absorbs.
//
// Priced server-side from the carrier, never taken from the request: a fee a client can name is a
// fee a client can set to zero. And it is quoted *before* the money is asked for, which also means
// a seller with no collection point fails the checkout rather than failing after the buyer has paid.
func (s *Service) quoteShipping(ctx context.Context, option string, sellerID int64,
	to domain.AddressSnapshot, lines []transport.ItemMetadata) (int64, error) {
	pickup, err := s.pickupSnapshot(ctx, sellerID)
	if err != nil {
		return 0, err
	}
	return s.quoteCarrier(ctx, option, pickup, to, lines)
}

// quoteCarrier is the call itself, with the pickup already read — so a page that prices every
// carrier reads the seller's collection point once instead of once per option.
func (s *Service) quoteCarrier(ctx context.Context, option string, from, to domain.AddressSnapshot,
	lines []transport.ItemMetadata) (int64, error) {
	quote, err := s.transport.Quote(ctx, transport.QuoteParams{
		Items:       lines,
		FromAddress: addressLine(from),
		ToAddress:   addressLine(to),
		Option:      option,
	})
	if err != nil {
		return 0, fmt.Errorf("quote shipping: %w", err)
	}
	if quote.Cost < 0 {
		return 0, domain.ErrShippingQuoteInvalid
	}
	return quote.Cost, nil
}

// addressLine is what a courier is handed: the administrative codes, not the display name. A
// carrier prices by district, and the name on the parcel does not change the fee.
func addressLine(a domain.AddressSnapshot) string {
	parts := []string{a.Country, a.ProvinceCode}
	if a.DistrictCode != nil {
		parts = append(parts, *a.DistrictCode)
	}
	parts = append(parts, a.WardCode)
	if a.AddressDetail != nil {
		parts = append(parts, *a.AddressDetail)
	}
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), ", ")
}
