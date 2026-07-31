package trust_test

import (
	"context"
	"slices"
	"time"

	"shopnexus/internal/module/common"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
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
	reports    map[int64]domain.Report
	// resources is this module's own resource table: an id absent from it names no confirmed
	// upload, which is what ErrAttachmentNotFound is about.
	resources map[int64]bool
	nextID    int64
	// ratingSync records what was pushed to catalog's cache, per listing.
	ratingSync map[int64][2]float64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		feedback: map[int64]domain.Feedback{}, reputation: map[[2]any]domain.Reputation{},
		reviews: map[int64]domain.Review{}, replies: map[int64]domain.ReviewReply{},
		votes: map[[2]int64]int16{}, reports: map[int64]domain.Report{},
		resources: map[int64]bool{}, ratingSync: map[int64][2]float64{},
	}
}

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
		if !filter.Cursor.Before.IsZero() && !row.CreatedAt.Before(filter.Cursor.Before) {
			continue
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b domain.Feedback) int { return int(b.ID - a.ID) })
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

func (f *fakeRepo) AddOrderOutcome(_ context.Context, buyerID, sellerID int64, completed bool) error {
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

func (f *fakeRepo) InsertReview(_ context.Context, v *domain.Review, sellerID int64) error {
	for _, existing := range f.reviews {
		if existing.ListingID == v.ListingID && existing.AuthorID == v.AuthorID &&
			existing.OrderID == v.OrderID {
			return domain.ErrReviewExists
		}
	}
	v.ID = f.next()
	v.CreatedAt = time.Now()
	f.reviews[v.ID] = *v
	f.addReviewRating(sellerID, int64(v.Rating), 1)
	return nil
}

func (f *fakeRepo) addReviewRating(sellerID, sum, count int64) {
	rep := f.reputation[key(sellerID, domain.RoleSeller)]
	rep.AccountID, rep.Role = sellerID, domain.RoleSeller
	rep.ReviewRatingSum += sum
	rep.ReviewRatingCount += count
	rep.UpdatedAt = time.Now()
	f.reputation[key(sellerID, domain.RoleSeller)] = rep
}

func (f *fakeRepo) FindReview(_ context.Context, id int64) (domain.Review, error) {
	row, ok := f.reviews[id]
	if !ok {
		return domain.Review{}, domain.ErrReviewNotFound
	}
	return row, nil
}

func (f *fakeRepo) ListReviews(_ context.Context, filter port.ReviewFilter) ([]domain.Review, error) {
	var out []domain.Review
	for _, row := range f.reviews {
		if filter.ListingID != 0 && row.ListingID != filter.ListingID {
			continue
		}
		if filter.AuthorID != 0 && row.AuthorID != filter.AuthorID {
			continue
		}
		if filter.Rating != 0 && row.Rating != filter.Rating {
			continue
		}
		if !filter.Cursor.Before.IsZero() && !row.CreatedAt.Before(filter.Cursor.Before) {
			continue
		}
		out = append(out, row)
	}
	if filter.Sort == domain.ReviewSortHelpful {
		slices.SortFunc(out, func(a, b domain.Review) int {
			if a.HelpfulCount != b.HelpfulCount {
				return int(b.HelpfulCount - a.HelpfulCount)
			}
			return int(b.ID - a.ID)
		})
	} else {
		slices.SortFunc(out, func(a, b domain.Review) int { return int(b.ID - a.ID) })
	}
	return trim(out, filter.Cursor.Limit), nil
}

func (f *fakeRepo) SaveReview(_ context.Context, v domain.Review, sellerID int64, ratingDelta int64) error {
	if _, ok := f.reviews[v.ID]; !ok {
		return domain.ErrReviewNotFound
	}
	f.reviews[v.ID] = v
	if ratingDelta != 0 {
		f.addReviewRating(sellerID, ratingDelta, 0)
	}
	return nil
}

func (f *fakeRepo) DeleteReview(_ context.Context, id int64, sellerID int64, rating int16) error {
	if _, ok := f.reviews[id]; !ok {
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
	f.addReviewRating(sellerID, -int64(rating), -1)
	return nil
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

// --- reports ---

func (f *fakeRepo) InsertReport(_ context.Context, r *domain.Report) error {
	for _, existing := range f.reports {
		if existing.ReporterID == r.ReporterID && existing.RefType == r.RefType &&
			existing.RefID == r.RefID && !existing.Resolved() {
			return domain.ErrReportExists
		}
	}
	r.ID = f.next()
	r.CreatedAt = time.Now()
	f.reports[r.ID] = *r
	return nil
}

func (f *fakeRepo) FindReport(_ context.Context, id int64) (domain.Report, error) {
	row, ok := f.reports[id]
	if !ok {
		return domain.Report{}, domain.ErrReportNotFound
	}
	return row, nil
}

func (f *fakeRepo) ListReports(_ context.Context, filter port.ReportFilter) ([]domain.Report, error) {
	var out []domain.Report
	for _, row := range f.reports {
		if filter.ReporterID != 0 && row.ReporterID != filter.ReporterID {
			continue
		}
		if len(filter.Statuses) > 0 && !slices.Contains(filter.Statuses, row.Status) {
			continue
		}
		if filter.RefType != "" && row.RefType != filter.RefType {
			continue
		}
		if filter.Reason != "" && row.Reason != filter.Reason {
			continue
		}
		out = append(out, row)
	}
	// The queue is worked oldest first; a reporter reads their own newest first.
	if filter.ReporterID == 0 {
		slices.SortFunc(out, func(a, b domain.Report) int { return int(a.ID - b.ID) })
	} else {
		slices.SortFunc(out, func(a, b domain.Report) int { return int(b.ID - a.ID) })
	}
	return trim(out, filter.Cursor.Limit), nil
}

// SaveReport is guarded by the status it moves from, so a stale read loses.
func (f *fakeRepo) SaveReport(_ context.Context, r domain.Report, from []string) error {
	stored, ok := f.reports[r.ID]
	if !ok || !slices.Contains(from, stored.Status) {
		return domain.ErrReportResolved
	}
	f.reports[r.ID] = r
	return nil
}

func (f *fakeRepo) CountOpenAgainst(_ context.Context, refType string, refID int64) (int64, error) {
	var count int64
	for _, row := range f.reports {
		if row.RefType == refType && row.RefID == refID && !row.Resolved() {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepo) FindResources(_ context.Context, ids []int64) ([]common.Resource, error) {
	out := make([]common.Resource, 0, len(ids))
	for _, one := range ids {
		if f.resources[one] {
			out = append(out, common.Resource{ID: one, Provider: "minio", ObjectKey: "k", Mime: "image/jpeg"})
		}
	}
	return out, nil
}

func trim[T any](rows []T, limit int) []T {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}
