package trust_test

import (
	"context"
	"slices"
	"strconv"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/provider/storage"
	"shopnexus/internal/shared/errx"
)

// fakeRepo is an in-memory port.Repository that keeps the rules the schema keeps: one
// feedback per (order, direction), one review per (listing, author, order), one vote per
// (review, account), one open report per (reporter, target) — and the same reveal-and-count
// coupling the real adapter does in a transaction. A test that could pass against a fake
// which does not refuse what the database refuses is not testing the service.
type fakeRepo struct {
	feedback   map[int64]domain.Feedback
	reputation map[[2]any]domain.Reputation
	reviews    map[int64]domain.Review
	replies    map[int64]domain.ReviewReply
	votes      map[[2]int64]int16
	tickets    map[int64]domain.Ticket
	// outcomes is the dedupe key AddOrderOutcome writes with the bump: the settled event is
	// at-least-once, so a second delivery of one order must count nothing.
	outcomes map[int64]bool
	nextID   int64
	// ratingSync records what was pushed to catalog's cache, per listing.
	ratingSync map[int64][2]float64
	// saveTicketErr stands in for a database that is simply unreachable, which a service must
	// not report as "somebody else claimed it".
	saveTicketErr error
	// countCalls is how many round trips the queue spent on the pattern, which is the N+1 a
	// page of the moderator queue used to pay per row.
	countCalls int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		feedback: map[int64]domain.Feedback{}, reputation: map[[2]any]domain.Reputation{},
		reviews: map[int64]domain.Review{}, replies: map[int64]domain.ReviewReply{},
		votes: map[[2]int64]int16{}, tickets: map[int64]domain.Ticket{},
		outcomes:   map[int64]bool{},
		ratingSync: map[int64][2]float64{},
	}
}

// errCountersNegative is the schema's "reputation_counters_non_negative" in the fake. A fake
// that absorbs a delta the database refuses lets a service ship a 500: that is exactly how a
// seller id of 0 passed every unit test here.
var errCountersNegative = errx.NewError(500, "counters_negative", "a reputation counter would go negative")

func (f *fakeRepo) next() int64 { f.nextID++; return f.nextID }

func key(accountID int64, role string) [2]any { return [2]any{accountID, role} }

// --- feedback ---

func (f *fakeRepo) InsertFeedback(_ context.Context, fb *domain.Feedback) error {
	for _, existing := range f.feedback {
		if existing.OrderID == fb.OrderID && existing.Direction == fb.Direction {
			return domain.ErrFeedbackExists
		}
	}
	fb.ID = f.next()
	fb.CreatedAt = time.Now()
	f.feedback[fb.ID] = *fb
	// The pair completing is what reveals both, as the adapter does in one transaction.
	other, ok := f.byDirection(fb.OrderID, domain.Opposite(fb.Direction))
	if !ok {
		return nil
	}
	now := time.Now()
	f.publish(fb.ID, now)
	f.publish(other.ID, now)
	*fb = f.feedback[fb.ID]
	return nil
}

func (f *fakeRepo) byDirection(orderID int64, direction string) (domain.Feedback, bool) {
	for _, row := range f.feedback {
		if row.OrderID == orderID && row.Direction == direction {
			return row, true
		}
	}
	return domain.Feedback{}, false
}

func (f *fakeRepo) FindFeedback(_ context.Context, orderID int64, direction string) (domain.Feedback, error) {
	if row, ok := f.byDirection(orderID, direction); ok {
		return row, nil
	}
	return domain.Feedback{}, domain.ErrFeedbackNotFound
}

func (f *fakeRepo) OrderFeedback(_ context.Context, orderID int64) ([]domain.Feedback, error) {
	var out []domain.Feedback
	for _, row := range f.feedback {
		if row.OrderID == orderID {
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b domain.Feedback) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f *fakeRepo) ListFeedback(_ context.Context, filter port.FeedbackFilter) ([]domain.Feedback, error) {
	var out []domain.Feedback
	for _, row := range f.feedback {
		if row.RateeID != filter.RateeID || !row.Published() {
			continue
		}
		if filter.Role != "" && domain.RoleRated(row.Direction) != filter.Role {
			continue
		}
		if filter.Cursor.BeforeID != 0 && !before(timeKey(row.CreatedAt, row.ID), cursorKey(filter.Cursor)) {
			continue
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b domain.Feedback) int {
		return descending(timeKey(a.CreatedAt, a.ID), timeKey(b.CreatedAt, b.ID))
	})
	return trim(out, filter.Cursor.Limit), nil
}

func (f *fakeRepo) DueFeedback(_ context.Context, now time.Time, limit int) ([]domain.Feedback, error) {
	var out []domain.Feedback
	for _, row := range f.feedback {
		if !row.Published() && row.CreatedAt.Add(domain.BlindWindow).Before(now) {
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b domain.Feedback) int { return int(a.ID - b.ID) })
	return trim(out, limit), nil
}

func (f *fakeRepo) PublishFeedback(_ context.Context, id int64, at time.Time) error {
	f.publish(id, at)
	return nil
}

// publish reveals a row and counts it, refusing to count twice — the `published_at IS NULL`
// guard the adapter writes into the UPDATE.
func (f *fakeRepo) publish(id int64, at time.Time) {
	row, ok := f.feedback[id]
	if !ok || row.Published() {
		return
	}
	row.Publish(at)
	f.feedback[id] = row
	role := domain.RoleRated(row.Direction)
	rep := f.reputation[key(row.RateeID, role)]
	rep.AccountID, rep.Role = row.RateeID, role
	rep.RatingSum += int64(row.Rating)
	rep.RatingCount++
	rep.UpdatedAt = at
	f.reputation[key(row.RateeID, role)] = rep
}

// --- reputation ---

func (f *fakeRepo) FindReputation(_ context.Context, accountID int64, role string) (domain.Reputation, error) {
	rep, ok := f.reputation[key(accountID, role)]
	if !ok {
		return domain.Reputation{AccountID: accountID, Role: role}, nil
	}
	return rep, nil
}

// AddOrderOutcome claims the order id first, as the adapter does in the same transaction as the
// bump: the settled event is at-least-once, and a counter has no idempotence of its own.
func (f *fakeRepo) AddOrderOutcome(_ context.Context, orderID, buyerID, sellerID int64, completed bool) error {
	if f.outcomes[orderID] {
		return nil
	}
	f.outcomes[orderID] = true
	for _, party := range []struct {
		id   int64
		role string
	}{{buyerID, domain.RoleBuyer}, {sellerID, domain.RoleSeller}} {
		rep := f.reputation[key(party.id, party.role)]
		rep.AccountID, rep.Role = party.id, party.role
		if completed {
			rep.CompletedOrders++
		} else {
			rep.CancelledOrders++
		}
		rep.UpdatedAt = time.Now()
		f.reputation[key(party.id, party.role)] = rep
	}
	return nil
}

func (f *fakeRepo) ReviewAverage(_ context.Context, listingID int64) (float64, int64, error) {
	var sum, count int64
	for _, row := range f.reviews {
		if row.ListingID == listingID {
			sum += int64(row.Rating)
			count++
		}
	}
	if count == 0 {
		return 0, 0, nil
	}
	return float64(sum) / float64(count), count, nil
}

// --- reviews ---

func (f *fakeRepo) InsertReview(_ context.Context, v *domain.Review) error {
	for _, existing := range f.reviews {
		if existing.ListingID == v.ListingID && existing.AuthorID == v.AuthorID &&
			existing.OrderID == v.OrderID {
			return domain.ErrReviewExists
		}
	}
	v.ID = f.next()
	v.CreatedAt = time.Now()
	f.reviews[v.ID] = *v
	return f.addReviewRating(v.SellerID, int64(v.Rating), 1)
}

// addReviewRating keeps the CHECK the schema keeps: a delta that would take a counter below
// zero is refused rather than stored.
func (f *fakeRepo) addReviewRating(sellerID, sum, count int64) error {
	rep := f.reputation[key(sellerID, domain.RoleSeller)]
	if rep.ReviewRatingSum+sum < 0 || rep.ReviewRatingCount+count < 0 {
		return errCountersNegative
	}
	rep.AccountID, rep.Role = sellerID, domain.RoleSeller
	rep.ReviewRatingSum += sum
	rep.ReviewRatingCount += count
	rep.UpdatedAt = time.Now()
	f.reputation[key(sellerID, domain.RoleSeller)] = rep
	return nil
}

func (f *fakeRepo) FindReview(_ context.Context, id int64) (domain.Review, error) {
	row, ok := f.reviews[id]
	if !ok {
		return domain.Review{}, domain.ErrReviewNotFound
	}
	return row, nil
}

// ListReviews pages on the key it orders by, as the SQL does: the cursor is the pair
// (key, id), so a boundary tie skips nothing and a helpful-sorted page is not bounded by a
// timestamp it never ordered on.
func (f *fakeRepo) ListReviews(_ context.Context, filter port.ReviewFilter) ([]domain.Review, error) {
	helpful := filter.Sort == domain.ReviewSortHelpful
	after := func(row domain.Review) bool {
		if filter.Cursor.BeforeID == 0 {
			return true
		}
		if helpful {
			return before([2]int64{row.HelpfulCount, row.ID},
				[2]int64{filter.Cursor.BeforeCount, filter.Cursor.BeforeID})
		}
		return before([2]int64{row.CreatedAt.UnixNano(), row.ID},
			[2]int64{filter.Cursor.Before.UnixNano(), filter.Cursor.BeforeID})
	}
	var out []domain.Review
	for _, row := range f.reviews {
		if filter.ListingID != 0 && row.ListingID != filter.ListingID {
			continue
		}
		if filter.Rating != 0 && row.Rating != filter.Rating {
			continue
		}
		if !after(row) {
			continue
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b domain.Review) int {
		if helpful {
			return descending([2]int64{a.HelpfulCount, a.ID}, [2]int64{b.HelpfulCount, b.ID})
		}
		return descending([2]int64{a.CreatedAt.UnixNano(), a.ID},
			[2]int64{b.CreatedAt.UnixNano(), b.ID})
	})
	return trim(out, filter.Cursor.Limit), nil
}

// SaveReview and DeleteReview derive the aggregate's move from the stored row, which is what
// the adapter reads under FOR UPDATE — a delta computed from the caller's older copy is the
// one that moves a reputation twice.
func (f *fakeRepo) SaveReview(_ context.Context, v domain.Review) error {
	stored, ok := f.reviews[v.ID]
	if !ok {
		return domain.ErrReviewNotFound
	}
	f.reviews[v.ID] = v
	delta := int64(v.Rating) - int64(stored.Rating)
	if delta == 0 {
		return nil
	}
	return f.addReviewRating(stored.SellerID, delta, 0)
}

func (f *fakeRepo) DeleteReview(_ context.Context, id int64) error {
	stored, ok := f.reviews[id]
	if !ok {
		return domain.ErrReviewNotFound
	}
	delete(f.reviews, id)
	// The replies and the votes go with it, as the cascade does.
	for replyID, reply := range f.replies {
		if reply.ReviewID == id {
			delete(f.replies, replyID)
		}
	}
	for pair := range f.votes {
		if pair[0] == id {
			delete(f.votes, pair)
		}
	}
	return f.addReviewRating(stored.SellerID, -int64(stored.Rating), -1)
}

// --- replies ---

func (f *fakeRepo) InsertReply(_ context.Context, r *domain.ReviewReply) error {
	review, ok := f.reviews[r.ReviewID]
	if !ok {
		return domain.ErrReviewNotFound
	}
	r.ID = f.next()
	r.CreatedAt = time.Now()
	f.replies[r.ID] = *r
	review.ReplyCount++
	f.reviews[review.ID] = review
	return nil
}

func (f *fakeRepo) FindReply(_ context.Context, id int64) (domain.ReviewReply, error) {
	row, ok := f.replies[id]
	if !ok {
		return domain.ReviewReply{}, domain.ErrReplyNotFound
	}
	return row, nil
}

func (f *fakeRepo) ListReplies(_ context.Context, reviewIDs []int64, limit int) (map[int64][]domain.ReviewReply, error) {
	out := map[int64][]domain.ReviewReply{}
	for _, row := range f.replies {
		if slices.Contains(reviewIDs, row.ReviewID) {
			out[row.ReviewID] = append(out[row.ReviewID], row)
		}
	}
	for reviewID, thread := range out {
		slices.SortFunc(thread, func(a, b domain.ReviewReply) int { return int(a.ID - b.ID) })
		out[reviewID] = trim(thread, limit)
	}
	return out, nil
}

func (f *fakeRepo) DeleteReply(_ context.Context, id int64) error {
	row, ok := f.replies[id]
	if !ok {
		return domain.ErrReplyNotFound
	}
	delete(f.replies, id)
	if review, ok := f.reviews[row.ReviewID]; ok {
		review.ReplyCount--
		f.reviews[review.ID] = review
	}
	return nil
}

// --- votes ---

func (f *fakeRepo) PutVote(_ context.Context, v domain.ReviewVote) (port.VoteTally, error) {
	review, ok := f.reviews[v.ReviewID]
	if !ok {
		return port.VoteTally{}, domain.ErrReviewNotFound
	}
	previous := f.votes[[2]int64{v.ReviewID, v.AccountID}]
	f.votes[[2]int64{v.ReviewID, v.AccountID}] = v.Vote
	helpful, notHelpful := domain.VoteDelta(previous, v.Vote)
	review.HelpfulCount += helpful
	review.NotHelpfulCount += notHelpful
	f.reviews[review.ID] = review
	vote := v.Vote
	return port.VoteTally{
		Helpful: review.HelpfulCount, NotHelpful: review.NotHelpfulCount, MyVote: &vote,
	}, nil
}

func (f *fakeRepo) DeleteVote(_ context.Context, reviewID, accountID int64) (port.VoteTally, error) {
	review, ok := f.reviews[reviewID]
	if !ok {
		return port.VoteTally{}, domain.ErrReviewNotFound
	}
	previous, voted := f.votes[[2]int64{reviewID, accountID}]
	if !voted {
		return port.VoteTally{}, domain.ErrVoteNotFound
	}
	delete(f.votes, [2]int64{reviewID, accountID})
	helpful, notHelpful := domain.VoteDelta(previous, 0)
	review.HelpfulCount += helpful
	review.NotHelpfulCount += notHelpful
	f.reviews[review.ID] = review
	return port.VoteTally{Helpful: review.HelpfulCount, NotHelpful: review.NotHelpfulCount}, nil
}

func (f *fakeRepo) MyVotes(_ context.Context, accountID int64, reviewIDs []int64) (map[int64]int16, error) {
	out := map[int64]int16{}
	if accountID == 0 {
		return out, nil
	}
	for pair, vote := range f.votes {
		if pair[1] == accountID && slices.Contains(reviewIDs, pair[0]) {
			out[pair[0]] = vote
		}
	}
	return out, nil
}

// --- tickets ---

// InsertTicket holds the rule the partial unique index does: one open ticket per requester per
// target, because a second complaint about the same listing is the same complaint.
func (f *fakeRepo) InsertTicket(_ context.Context, t *domain.Ticket) error {
	for _, existing := range f.tickets {
		if existing.RequesterID != t.RequesterID || existing.Resolved() {
			continue
		}
		if t.RefType == nil || existing.RefType == nil || t.RefID == nil || existing.RefID == nil {
			continue
		}
		if *existing.RefType == *t.RefType && *existing.RefID == *t.RefID {
			return domain.ErrTicketExists
		}
	}
	t.ID = f.next()
	t.CreatedAt = time.Now()
	f.tickets[t.ID] = *t
	return nil
}

func (f *fakeRepo) FindTicket(_ context.Context, id int64) (domain.Ticket, error) {
	row, ok := f.tickets[id]
	if !ok {
		return domain.Ticket{}, domain.ErrTicketNotFound
	}
	return row, nil
}

// OpenTicketsAgainst is how a module holding the target — order, with a refund it just decided —
// finds the tickets to close. Oldest first, like the adapter, and every one of them: the index only
// holds one open ticket per requester, so both parties to a refund may have raised one.
func (f *fakeRepo) OpenTicketsAgainst(_ context.Context, refType string, refID int64) ([]domain.Ticket, error) {
	var out []domain.Ticket
	for _, row := range f.tickets {
		if row.Resolved() || row.RefType == nil || row.RefID == nil {
			continue
		}
		if *row.RefType == refType && *row.RefID == refID {
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b domain.Ticket) int {
		return -descending(timeKey(a.CreatedAt, a.ID), timeKey(b.CreatedAt, b.ID))
	})
	return out, nil
}

// ListTickets pages both directions the adapter serves: the queue is worked oldest first, a
// requester reads their own newest first, and each compares (created_at, id) as a tuple.
func (f *fakeRepo) ListTickets(_ context.Context, filter port.TicketFilter) ([]domain.Ticket, error) {
	queue := filter.RequesterID == 0
	var out []domain.Ticket
	for _, row := range f.tickets {
		if filter.RequesterID != 0 && row.RequesterID != filter.RequesterID {
			continue
		}
		if len(filter.Statuses) > 0 && !slices.Contains(filter.Statuses, row.Status) {
			continue
		}
		if filter.Kind != "" && row.Kind != filter.Kind {
			continue
		}
		if filter.Cursor.BeforeID != 0 {
			key, cursor := timeKey(row.CreatedAt, row.ID), cursorKey(filter.Cursor)
			// Strict in both directions, or the row the cursor names is served twice.
			past := before(key, cursor)
			if queue {
				past = before(cursor, key)
			}
			if !past {
				continue
			}
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b domain.Ticket) int {
		order := descending(timeKey(a.CreatedAt, a.ID), timeKey(b.CreatedAt, b.ID))
		if queue {
			return -order
		}
		return order
	})
	return trim(out, filter.Cursor.Limit), nil
}

// SaveTicket is guarded by the status it moves from, so a stale read loses. saveTicketErr is the
// other way it can fail: a database that is not there at all.
func (f *fakeRepo) SaveTicket(_ context.Context, t domain.Ticket, from []string) error {
	if f.saveTicketErr != nil {
		return f.saveTicketErr
	}
	stored, ok := f.tickets[t.ID]
	if !ok || !slices.Contains(from, stored.Status) {
		return domain.ErrTicketResolved
	}
	f.tickets[t.ID] = t
	return nil
}

func (f *fakeRepo) CountOpenAgainst(_ context.Context, targets []port.TicketTarget) (map[port.TicketTarget]int64, error) {
	f.countCalls++
	out := make(map[port.TicketTarget]int64, len(targets))
	for _, row := range f.tickets {
		if row.RefType == nil || row.RefID == nil {
			continue
		}
		target := port.TicketTarget{RefType: *row.RefType, RefID: *row.RefID}
		if slices.Contains(targets, target) && !row.Resolved() {
			out[target]++
		}
	}
	return out, nil
}

// fakeUploads is the upload seam a service test needs: it records a slot per resource id and
// resolves a confirmed one, refusing what the real store refuses — an unconfirmed id, another
// uploader's slot, and bytes that never arrived.
type fakeUploads struct {
	nextID int64
	// slots is what Presign handed out, pending is whether it has been confirmed, and owner is
	// who may confirm it.
	slots     map[int64]bool
	owner     map[int64]int64
	confirmed map[int64]bool
	// arrived is whether the client actually uploaded. A confirm without it is refused, which
	// is what stops a row rendering as a broken image.
	arrived map[int64]bool
}

func newFakeUploads() *fakeUploads {
	return &fakeUploads{
		slots: map[int64]bool{}, owner: map[int64]int64{},
		confirmed: map[int64]bool{}, arrived: map[int64]bool{},
	}
}

func (f *fakeUploads) Presign(_ context.Context, uploaderID int64, _ string, req common.UploadRequest) (common.UploadSlot, error) {
	f.nextID++
	f.slots[f.nextID] = true
	f.owner[f.nextID] = uploaderID
	return common.UploadSlot{
		ResourceID: f.nextID,
		URL:        "https://store.test/put/" + strconv.FormatInt(f.nextID, 10),
		ExpiresAt:  time.Now().Add(15 * time.Minute),
	}, nil
}

func (f *fakeUploads) Confirm(_ context.Context, uploaderID, resourceID int64) (common.Resource, error) {
	if !f.slots[resourceID] || f.confirmed[resourceID] || f.owner[resourceID] != uploaderID {
		return common.Resource{}, common.ErrResourceNotFound
	}
	if !f.arrived[resourceID] {
		return common.Resource{}, storage.ErrObjectNotFound
	}
	f.confirmed[resourceID] = true
	return common.Resource{ID: resourceID, Provider: "test", ObjectKey: "k", Mime: "image/jpeg"}, nil
}

func (f *fakeUploads) Resolve(_ context.Context, ids []int64) (map[int64]common.ResourceDTO, error) {
	out := make(map[int64]common.ResourceDTO, len(ids))
	for _, one := range ids {
		if !f.confirmed[one] {
			continue
		}
		out[one] = common.Resource{
			ID: one, Provider: "test", ObjectKey: "k", Mime: "image/jpeg",
			URL: "https://store.test/get/" + strconv.FormatInt(one, 10),
		}.ToDTO()
	}
	return out, nil
}

// A cursor here is the pair (ordering key, row id), compared as a tuple — which is what makes
// a boundary tie skip nothing when two rows share a timestamp exactly.
func timeKey(at time.Time, rowID int64) [2]int64 { return [2]int64{at.UnixNano(), rowID} }

func cursorKey(c port.CursorFilter) [2]int64 {
	return [2]int64{c.Before.UnixNano(), c.BeforeID}
}

func before(a, b [2]int64) bool {
	return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1])
}

func descending(a, b [2]int64) int {
	switch {
	case before(a, b):
		return 1
	case before(b, a):
		return -1
	}
	return 0
}

func trim[T any](rows []T, limit int) []T {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

// Bytes is what the suggestion route reads a photo through: only a confirmed upload has any, and the
// content is a stand-in — what a test checks is which ids reached the model, not the pixels.
func (f *fakeUploads) Bytes(_ context.Context, ids []int64) ([]common.Blob, error) {
	out := make([]common.Blob, 0, len(ids))
	for _, id := range ids {
		if !f.confirmed[id] {
			continue
		}
		out = append(out, common.Blob{
			ResourceID: id, Mime: "image/jpeg",
			Data: []byte("photo-" + strconv.FormatInt(id, 10)),
		})
	}
	return out, nil
}
