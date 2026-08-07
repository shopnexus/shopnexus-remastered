// Package account implements accountapi.Service — the only place that orchestrates
// the account domain, its repository, the session store and the outbound providers.
package account

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/go-playground/validator/v10"

	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/domain"
	"shopnexus/internal/module/account/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/provider/kyc"
	"shopnexus/internal/provider/notify"
	"shopnexus/internal/provider/oauth"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/realtime"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

// One-time secrets live in Redis rather than in a table: each is read once, by one
// key, and then has to disappear — which is a TTL, not a row somebody has to sweep.
const (
	emailVerifyTTL   = 24 * time.Hour
	passwordResetTTL = time.Hour
	phoneCodeTTL     = 10 * time.Minute
	// throttleTTL is how long a repeated "send me that message again" is refused. It is
	// keyed per identifier and set before the account lookup, so the answer cannot be
	// used to tell an existing address from an unknown one.
	throttleTTL = time.Minute
)

// Key prefixes for those secrets, namespaced by module because the cache is shared.
const (
	emailVerifyPrefix   = "account:email-verify:"
	passwordResetPrefix = "account:password-reset:"
	phoneCodePrefix     = "account:contact-phone-code:"
	throttlePrefix      = "account:throttle:"
)

// The market this deployment serves, used only when a federated provider tells us
// nothing about a brand-new account and the profile columns are NOT NULL. The client
// corrects them with PATCH /me/profile, which is one round trip away.
const (
	defaultCountry  = "VN"
	defaultLocale   = "vi-VN"
	defaultTimezone = "Asia/Ho_Chi_Minh"
)

type Service struct {
	repo     port.Repository
	sessions *session.Store
	tokens   *token.Manager
	// cache holds the one-time secrets and the send throttles.
	cache  cache.Client
	notify notify.Client
	oauth  oauth.Verifier
	kyc    kyc.Client
	// uploads is this module's own resource table plus the object store. An avatar and an
	// identity scan share it — presigning per request is what keeps a scan from ever being
	// a public link, unlike the avatar it sits beside.
	uploads common.Uploads
	// v validates a request before the service acts on it. The handler checks the same struct,
	// but a service-to-service caller reaching this contract through accountapi.Service never
	// passes through a handler — and that caller is exactly the one no route test covers.
	v   *validator.Validate
	log *slog.Logger
	// fanout pushes the realtime facts in event.go to the socket the owning account may
	// have open. Best-effort: a write always commits whether or not anybody is listening.
	fanout realtime.Fanout
	// support memoises the desk account, resolved by its role. Seeded by a migration and never
	// edited, so one lookup answers every ticket thread this process opens.
	support atomic.Value
}

func NewService(
	repo port.Repository,
	sessions *session.Store,
	tokens *token.Manager,
	c cache.Client,
	notifier notify.Client,
	verifier oauth.Verifier,
	kycClient kyc.Client,
	uploads common.Uploads,
	v *validator.Validate,
	log *slog.Logger,
	fanout realtime.Fanout,
) *Service {
	return &Service{
		repo:     repo,
		sessions: sessions,
		tokens:   tokens,
		cache:    c,
		notify:   notifier,
		oauth:    verifier,
		kyc:      kycClient,
		uploads:  uploads,
		v:        v,
		log:      log,
		fanout:   fanout,
	}
}

// notifyRealtime pushes a fact to one account, best-effort. Named apart from the
// notify.Client field this package already has of that name — this one is the socket,
// not email/SMS.
//
// A realtime failure never fails the command: the row is committed by the time this runs,
// so the alternative is answering 500 for a write that happened. The client re-reads on
// reconnect, which is what covers a dropped event.
func notifyRealtime[T any](ctx context.Context, s *Service, accountID int64, e realtime.Event[T], data T) {
	if err := realtime.Notify(ctx, s.fanout, accountID, e, data); err != nil {
		s.log.Warn("realtime notify failed", "code", e.Code, "account_id", accountID, "err", err)
	}
}

var _ accountapi.Service = (*Service)(nil)

// CreateUpload reserves a row and a signed slot for an avatar or an identity scan. The
// client PUTs the bytes at the store and confirms; until then the resource resolves to
// nothing, so a half-finished upload cannot be attached to anything.
func (s *Service) CreateUpload(ctx context.Context, req accountapi.CreateUploadRequest) (common.UploadSlotDTO, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return common.UploadSlotDTO{}, err
	}
	slot, err := s.uploads.Presign(ctx, req.ActorID.Int64(), req.Kind, common.UploadRequest{
		Filename: req.Filename, Mime: req.Mime, Size: req.Size,
	})
	if err != nil {
		return common.UploadSlotDTO{}, err
	}
	return slot.ToDTO(), nil
}

// ConfirmUpload makes the upload real, with the size the store reports rather than the one
// the client declared. Scoped to the uploader: a resource id is guessable, and confirming
// somebody else's slot would be claiming their upload.
func (s *Service) ConfirmUpload(ctx context.Context, req common.ConfirmUploadRequest) (common.ResourceDTO, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return common.ResourceDTO{}, err
	}
	res, err := s.uploads.Confirm(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return common.ResourceDTO{}, err
	}
	return res.ToDTO(), nil
}

// --- shared loaders ---

// actor loads the signed-in caller. It is a plain read: a request that got past the
// gateway has a live session, and every bulk revocation drops sessions, so there is no
// second status check to make here.
func (s *Service) actor(ctx context.Context, actorID id.ID[id.Account]) (*domain.Account, error) {
	a, err := s.repo.Get(ctx, actorID.Int64())
	if err != nil {
		return nil, fmt.Errorf("get actor account: %w", err)
	}
	return a, nil
}

// requireModerator and requireAdmin are enforced here rather than in the handler: the
// caller's role is a column in this module's table, so a handler could only learn it by
// asking this service anyway. An admin passes every moderator check — a role that
// outranks another and still gets refused is a bug waiting to be filed.
func (s *Service) requireModerator(ctx context.Context, actorID id.ID[id.Account]) (*domain.Account, error) {
	a, err := s.actor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if a.Role != domain.RoleModerator && a.Role != domain.RoleAdmin {
		return nil, domain.ErrModeratorRequired
	}
	return a, nil
}

func (s *Service) requireAdmin(ctx context.Context, actorID id.ID[id.Account]) (*domain.Account, error) {
	a, err := s.actor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if a.Role != domain.RoleAdmin {
		return nil, domain.ErrAdminRequired
	}
	return a, nil
}

// GetSupportAccount answers the desk's own account — the second side of every ticket thread —
// resolved by its role, which is the one thing about that row nobody can register. Memoised after
// the first read: a migration seeds it and no route changes it, so a ticket message should not pay
// a query for it. A deployment that never seeded it fails here rather than picking an account.
func (s *Service) GetSupportAccount(ctx context.Context) (accountapi.AccountSummary, error) {
	if cached, ok := s.support.Load().(accountapi.AccountSummary); ok {
		return cached, nil
	}
	acc, err := s.repo.GetSupportAccount(ctx)
	if err != nil {
		return accountapi.AccountSummary{}, fmt.Errorf("read support account: %w", err)
	}
	out := accountapi.AccountSummary{ID: id.Of[id.Account](acc.ID), Name: acc.Profile.Name}
	s.support.Store(out)
	return out, nil
}

// --- DTO mapping ---

func (s *Service) toMe(ctx context.Context, a *domain.Account, identityVerified bool) accountapi.Me {
	return accountapi.Me{
		ID:               id.Of[id.Account](a.ID),
		Email:            a.Email,
		EmailVerified:    a.EmailVerified,
		Phone:            a.Phone,
		Username:         a.Username,
		Role:             string(a.Role),
		Status:           string(a.Status),
		HasPassword:      a.HasPassword(),
		IdentityVerified: identityVerified,
		Profile:          s.toProfile(ctx, a),
		CreatedAt:        a.CreatedAt,
	}
}

func (s *Service) toProfile(ctx context.Context, a *domain.Account) accountapi.Profile {
	p := a.Profile
	return accountapi.Profile{
		Name:        p.Name,
		Description: p.Description,
		Gender:      (*string)(p.Gender),
		DateOfBirth: optionalDate(p.DateOfBirth),
		Avatar:      s.avatar(ctx, p.AvatarResourceID),
		Country:     p.Country,
		Locale:      p.Locale,
		Timezone:    p.Timezone,
		CreatedAt:   a.CreatedAt,
	}
}

func toAdminAccount(a *domain.Account, identityVerified bool) accountapi.AdminAccount {
	return accountapi.AdminAccount{
		ID:               id.Of[id.Account](a.ID),
		Email:            a.Email,
		EmailVerified:    a.EmailVerified,
		Phone:            a.Phone,
		Username:         a.Username,
		Name:             a.Profile.Name,
		Role:             string(a.Role),
		Status:           string(a.Status),
		SuspendedUntil:   a.SuspendedUntil,
		SuspensionReason: a.SuspensionReason,
		IdentityVerified: identityVerified,
		CreatedAt:        a.CreatedAt,
	}
}

// summaryToAdminAccount is the same view built from a list row, which carries the display
// name instead of a whole profile.
func summaryToAdminAccount(s port.AccountSummary, identityVerified bool) accountapi.AdminAccount {
	return accountapi.AdminAccount{
		ID:               id.Of[id.Account](s.ID),
		Email:            s.Email,
		EmailVerified:    s.EmailVerified,
		Phone:            s.Phone,
		Username:         s.Username,
		Name:             s.Name,
		Role:             string(s.Role),
		Status:           string(s.Status),
		SuspendedUntil:   s.SuspendedUntil,
		SuspensionReason: s.SuspensionReason,
		IdentityVerified: identityVerified,
		CreatedAt:        s.CreatedAt,
	}
}

func toIdentityDocument(d domain.IdentityDocument) accountapi.IdentityDocument {
	return accountapi.IdentityDocument{
		ID:              id.Of[id.IdentityDocument](d.ID),
		DocType:         string(d.DocType),
		Provider:        d.Provider,
		Status:          string(d.Status),
		RejectionReason: d.RejectionReason,
		VerifiedAt:      d.VerifiedAt,
		ExpiresAt:       d.ExpiresAt,
		CreatedAt:       d.CreatedAt,
	}
}

// summariesByID is the keyed form, for a caller that pairs a row with its subject by
// account id. A profile that did not come back is simply absent, which the caller answers
// for explicitly instead of reading a zero id back out of a slice.
func (s *Service) summariesByID(ctx context.Context, profiles map[int64]domain.Profile) map[int64]accountapi.AccountSummary {
	ids := make([]int64, 0, len(profiles))
	for _, p := range profiles {
		if p.AvatarResourceID != nil {
			ids = append(ids, *p.AvatarResourceID)
		}
	}
	avatars := s.resolveResources(ctx, ids)
	out := make(map[int64]accountapi.AccountSummary, len(profiles))
	for accountID, p := range profiles {
		summary := accountapi.AccountSummary{ID: id.Of[id.Account](accountID), Name: p.Name}
		if p.AvatarResourceID != nil {
			summary.Avatar = avatars[*p.AvatarResourceID]
		}
		out[accountID] = summary
	}
	return out
}

// summaries is the ordered form, for a page whose order is the answer (a follower list).
// It is 1-1 with its input.
func (s *Service) summaries(ctx context.Context, profiles []domain.Profile) []accountapi.AccountSummary {
	ids := make([]int64, 0, len(profiles))
	for _, p := range profiles {
		if p.AvatarResourceID != nil {
			ids = append(ids, *p.AvatarResourceID)
		}
	}
	avatars := s.resolveResources(ctx, ids)
	out := make([]accountapi.AccountSummary, 0, len(profiles))
	for _, p := range profiles {
		summary := accountapi.AccountSummary{ID: id.Of[id.Account](p.ID), Name: p.Name}
		if p.AvatarResourceID != nil {
			summary.Avatar = avatars[*p.AvatarResourceID]
		}
		out = append(out, summary)
	}
	return out
}

// avatar resolves one image through the common module. A failure degrades to no avatar
// and a warning: the storage catalogue being down must not take a profile read with it.
func (s *Service) avatar(ctx context.Context, resourceID *int64) *common.ResourceDTO {
	if resourceID == nil {
		return nil
	}
	return s.resolveResources(ctx, []int64{*resourceID})[*resourceID]
}

// resolveResources resolves image ids through this module's own uploads — one query for the
// whole page, because a list of twenty sellers is twenty avatars. A missing one is left out
// rather than failing the page. Avatars were its first caller; identity scans are its second.
func (s *Service) resolveResources(ctx context.Context, resourceIDs []int64) map[int64]*common.ResourceDTO {
	out := map[int64]*common.ResourceDTO{}
	if len(resourceIDs) == 0 {
		return out
	}
	found, err := s.uploads.Resolve(ctx, resourceIDs)
	if err != nil {
		s.log.Warn("resolve avatars failed", "count", len(resourceIDs), "err", err)
		return out
	}
	for resourceID, dto := range found {
		out[resourceID] = &dto
	}
	return out
}

// --- small conversions ---

// optionalDate renders a date as a plain day, because that is what it is.
func optionalDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

// parseDate reads a plain date off the wire. An empty string is "no date" — that is how
// a cleared patch field arrives, and it is not a malformed one.
func parseDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, errx.NewValidationError("invalid field: date_of_birth", errx.Field{
			Field: "date_of_birth", Rule: "date", Message: "must be a date, e.g. 2001-02-03",
		})
	}
	return &t, nil
}

// --- one-time secrets ---

// mintSecret is the handle a verification or reset link carries: 32 random bytes,
// URL-safe, stored under its own key so redeeming it is a delete.
func mintSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// mintCode is the SMS variant: six digits, because it is typed in by hand. Drawn from
// crypto/rand all the same — a guessable code is the same as no code.
func mintCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("read random number: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *Service) putSecret(ctx context.Context, key string, accountID int64, ttl time.Duration) error {
	if err := s.cache.Set(ctx, key, accountID, ttl); err != nil {
		return fmt.Errorf("store one-time secret: %w", err)
	}
	return nil
}

// takeSecret redeems a one-time secret: read it, delete it, and answer notFound for
// anything that is not there — unknown, already used and expired are one answer.
func (s *Service) takeSecret(ctx context.Context, key string, notFound error) (int64, error) {
	var accountID int64
	if err := s.cache.Get(ctx, key, &accountID); err != nil {
		if errors.Is(err, cache.ErrCacheMiss) {
			return 0, notFound
		}
		return 0, fmt.Errorf("read one-time secret: %w", err)
	}
	if err := s.cache.Delete(ctx, key); err != nil {
		return 0, fmt.Errorf("delete used one-time secret: %w", err)
	}
	return accountID, nil
}

// throttle refuses a repeat send within throttleTTL. The key is set whether or not
// there was anything to send, so a 429 carries no information about who exists.
func (s *Service) throttle(ctx context.Context, scope, subject string) error {
	key := throttlePrefix + scope + ":" + subject
	exists, err := s.cache.Exists(ctx, key)
	if err != nil {
		return fmt.Errorf("read send throttle: %w", err)
	}
	if exists {
		return domain.ErrTooManyRequests
	}
	if err := s.cache.Set(ctx, key, time.Now().Unix(), throttleTTL); err != nil {
		return fmt.Errorf("set send throttle: %w", err)
	}
	return nil
}

// send delivers a transactional message, best-effort on purpose: the secret is already
// stored, so a provider that is down must not undo a registration or a password change
// — the caller asks again and gets a new message.
func (s *Service) send(ctx context.Context, m notify.Message) {
	if err := s.notify.Send(ctx, m); err != nil {
		s.log.Error("send notification failed", "kind", string(m.Kind), "err", err)
	}
}

// --- cursor ---

// encodeCursor and decodeCursor keep the feed's keyset bound opaque. It is a timestamp
// underneath, and publishing that invites a client to construct one; an opaque string
// keeps the pagination contract ours to change.
func encodeCursor(t time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano)))
}

func decodeCursor(s string) (time.Time, error) {
	invalid := errx.NewValidationError("invalid field: cursor", errx.Field{
		Field: "cursor", Rule: "cursor", Message: "must be a cursor from a previous page",
	})
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, invalid
	}
	t, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}, invalid
	}
	return t, nil
}
