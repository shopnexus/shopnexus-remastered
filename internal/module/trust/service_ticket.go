package trust

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/module/trust/domain"
	"shopnexus/internal/module/trust/port"
	"shopnexus/internal/shared/errx"
	"shopnexus/internal/shared/id"
)

// The queue's default slice: the unresolved tickets, which is exactly the predicate the partial
// index covers.
var openStatuses = []string{domain.StatusOpen, domain.StatusReviewing}

// prefixFor maps a ticket's ref_type to the id prefix that decodes its ref_id. The polymorphic
// reference stays a string on the wire because its kind is only known at run time.
func prefixFor(refType string) (string, bool) {
	switch refType {
	case domain.RefListing:
		return id.Prefix[id.Listing](), true
	case domain.RefAccount:
		return id.Prefix[id.Account](), true
	case domain.RefMessage:
		return id.Prefix[id.Message](), true
	case domain.RefReview:
		return id.Prefix[id.Review](), true
	case domain.RefReviewReply:
		return id.Prefix[id.ReviewReply](), true
	case domain.RefOrder:
		return id.Prefix[id.Order](), true
	case domain.RefRefund:
		return id.Prefix[id.Refund](), true
	}
	return "", false
}

// OpenTicket raises one: an abuse report, a refund the requester wants staff to decide, a payment
// that went wrong, a feature they wish existed. One route for all of them, because they are one
// thing — something submitted, and somebody who answers.
//
// The requester's own words and photos are not stored here: they become the first message of the
// conversation this opens, which is why raising a ticket needs no upload path of its own and why the
// requester's view of a ticket is simply a chat thread.
func (s *Service) OpenTicket(ctx context.Context, req trustapi.OpenTicketRequest) (trustapi.Ticket, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Ticket{}, err
	}
	refType, refID, err := s.ticketTarget(ctx, req)
	if err != nil {
		return trustapi.Ticket{}, err
	}
	reason, err := reasonOf(req.Kind, req.Reason)
	if err != nil {
		return trustapi.Ticket{}, err
	}
	// A refund dispute moves the refund before the ticket exists: order owns that status, and a
	// ticket about a refund staff may not decide is a queue entry with no possible answer. Order's
	// guard is what refuses the wrong party or the wrong moment, and it is idempotent, so a retry
	// escalates nothing twice.
	//
	// The target is the *order*, so this names the sale and order resolves the live case on it.
	// That is what puts every dispute about one order in one thread — a seller who escalates the
	// review window and then escalates again over what came back continues where they were.
	if req.Kind == domain.KindRefundDispute {
		if _, err := s.orders.EscalateRefund(ctx, orderapi.EscalateRefundRequest{
			ActorID: req.ActorID, OrderID: id.Of[id.Order](*refID),
		}); err != nil {
			return trustapi.Ticket{}, fmt.Errorf("escalate refund: %w", err)
		}
	}
	t, err := domain.NewTicket(req.ActorID.Int64(), req.Kind, req.Subject, refType, refID, reason)
	if err != nil {
		return trustapi.Ticket{}, err
	}
	if err := s.repo.InsertTicket(ctx, &t); err != nil {
		// A refund already with staff and no ticket naming it is invisible: the queue is the only
		// staff-facing list, and nothing sweeps for one. Only the requester retrying repairs it, so
		// this line is the alert.
		if req.Kind == domain.KindRefundDispute && !errors.Is(err, domain.ErrTicketExists) {
			s.log.Error("refund escalated with no ticket", "refund_id", req.RefID, "err", err)
		}
		return trustapi.Ticket{}, fmt.Errorf("insert ticket: %w", err)
	}
	// The thread, with what they wrote as its opening message. A failure here leaves a ticket with
	// no conversation, which the next read repairs — the row is the ticket, and losing the thread
	// must not lose the complaint.
	s.attachThread(ctx, &t, req.Body, req.Attachments)
	return toAPITicket(t), nil
}

// ticketTarget decodes what the ticket is about and checks it exists, against the kind: a report
// about a listing needs a listing, a refund dispute needs a refund, a feature request needs nothing.
func (s *Service) ticketTarget(ctx context.Context, req trustapi.OpenTicketRequest) (*string, *int64, error) {
	want := domain.RefKindOf(req.Kind)
	if want == "" {
		if req.RefID != "" {
			return nil, nil, domain.ErrTicketRefUnexpected
		}
		return nil, nil, nil
	}
	prefix, ok := prefixFor(want)
	if !ok {
		return nil, nil, domain.ErrTicketRefRequired
	}
	refID, err := id.ParseOpaque(prefix, req.RefID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.requireTarget(ctx, req.ActorID, want, refID); err != nil {
		return nil, nil, err
	}
	return &want, &refID, nil
}

// reasonOf keeps a reason to the kinds that have one, and refuses one anywhere else — symmetrically
// with the ref, because dropping a field the client sent answers 201 to a request nobody carried out.
func reasonOf(kind, reason string) (*string, error) {
	if domain.Reported(kind) != (reason != "") {
		return nil, domain.ErrTicketReasonMismatch
	}
	if reason == "" {
		return nil, nil
	}
	return &reason, nil
}

// attachThread opens the conversation and records it on the ticket. Best-effort and idempotent: the
// two rows are in different schemas, so one of them lands first, and a thread that is missing is a
// repair rather than a lost ticket.
func (s *Service) attachThread(ctx context.Context, t *domain.Ticket, body string, attachments []id.ID[id.Resource]) {
	thread, err := s.chat.OpenTicketThread(ctx, chatapi.OpenTicketThreadRequest{
		RequesterID: id.Of[id.Account](t.RequesterID),
		TicketID:    id.Of[id.Ticket](t.ID),
		Body:        body,
		Attachments: attachments,
	})
	if err != nil {
		s.log.Error("open ticket thread", "ticket_id", t.ID, "err", err)
		return
	}
	t.AttachThread(thread.ID.Int64())
	if err := s.repo.SaveTicket(ctx, *t, []string{t.Status}); err != nil {
		s.log.Error("record ticket thread", "ticket_id", t.ID, "err", err)
	}
}

// GetTicket is the requester's own. It repairs a missing thread on the way, so a ticket whose
// conversation was not written is answerable rather than mute.
func (s *Service) GetTicket(ctx context.Context, req trustapi.TicketRequest) (trustapi.Ticket, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Ticket{}, err
	}
	t, err := s.repo.FindTicket(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Ticket{}, fmt.Errorf("find ticket: %w", err)
	}
	if t.RequesterID != req.ActorID.Int64() {
		// Somebody else's is not found rather than forbidden: a ticket id is guessable, and
		// confirming one exists is already telling them something.
		return trustapi.Ticket{}, domain.ErrTicketNotFound
	}
	if t.ConversationID == nil {
		s.attachThread(ctx, &t, "", nil)
	}
	return toAPITicket(t), nil
}

// ListMyTickets is the requester's own list — everything they raised, in one place, which is the
// whole point of one table. They see the status of what they filed and never who else filed one.
func (s *Service) ListMyTickets(ctx context.Context, req trustapi.ListTicketsRequest) (trustapi.TicketPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.TicketPage{}, err
	}
	cursor, err := timeCursor(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.TicketPage{}, err
	}
	rows, err := s.repo.ListTickets(ctx, port.TicketFilter{
		RequesterID: req.ActorID.Int64(), Statuses: statusFilter(req.Status), Cursor: cursor,
	})
	if err != nil {
		return trustapi.TicketPage{}, fmt.Errorf("list tickets: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, ticketKey)
	data := make([]trustapi.Ticket, 0, len(rows))
	for _, row := range rows {
		data = append(data, toAPITicket(row))
	}
	return trustapi.TicketPage{Data: data, Meta: meta}, nil
}

// AdminListTickets is the moderator queue, oldest first — the order it is worked. It defaults to the
// unresolved slice rather than the whole table, and `kind` is how one queue is still workable: the
// abuse reports and the refund disputes are different jobs even though they are one row.
func (s *Service) AdminListTickets(ctx context.Context, req trustapi.AdminListTicketsRequest) (trustapi.AdminTicketPage, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.AdminTicketPage{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.AdminTicketPage{}, err
	}
	cursor, err := timeCursor(req.Cursor, req.Limit)
	if err != nil {
		return trustapi.AdminTicketPage{}, err
	}
	statuses := openStatuses
	if req.Status != "" {
		statuses = []string{req.Status}
	}
	rows, err := s.repo.ListTickets(ctx, port.TicketFilter{
		Statuses: statuses, Kind: req.Kind, Cursor: cursor,
	})
	if err != nil {
		return trustapi.AdminTicketPage{}, fmt.Errorf("list tickets: %w", err)
	}
	rows, meta := paginate(rows, req.Limit, ticketKey)
	// The queue is a work list: it carries the requester and the pattern, not the content it is
	// about, which the single-ticket read fetches.
	data, err := s.adminViews(ctx, req.ActorID, rows, false)
	if err != nil {
		return trustapi.AdminTicketPage{}, err
	}
	return trustapi.AdminTicketPage{Data: data, Meta: meta}, nil
}

// AdminGetTicket reads one case with the content it is about beside it, and how many others named
// the same target: a moderator decides on the pattern rather than one complaint.
func (s *Service) AdminGetTicket(ctx context.Context, req trustapi.TicketRequest) (trustapi.AdminTicket, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.AdminTicket{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.AdminTicket{}, err
	}
	t, err := s.repo.FindTicket(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.AdminTicket{}, fmt.Errorf("find ticket: %w", err)
	}
	entries, err := s.adminViews(ctx, req.ActorID, []domain.Ticket{t}, true)
	if err != nil {
		return trustapi.AdminTicket{}, err
	}
	return entries[0], nil
}

// AdminClaimTicket takes an open case for review, so two moderators do not work the same one. The
// requester is never told who: it changes nothing they can do, and support answers as the desk.
func (s *Service) AdminClaimTicket(ctx context.Context, req trustapi.TicketRequest) (trustapi.Ticket, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Ticket{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.Ticket{}, err
	}
	t, err := s.repo.FindTicket(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Ticket{}, fmt.Errorf("find ticket: %w", err)
	}
	if err := t.Claim(req.ActorID.Int64()); err != nil {
		return trustapi.Ticket{}, err
	}
	// Guarded by the status it moves from, so two moderators claiming at once means one wins.
	// Only that refusal is a conflict: a connection that dropped is not "already claimed", and
	// answering 409 to it sends a moderator looking for a colleague who was never there.
	if err := s.repo.SaveTicket(ctx, t, []string{domain.StatusOpen}); err != nil {
		if errors.Is(err, domain.ErrTicketResolved) {
			return trustapi.Ticket{}, domain.ErrTicketNotClaimable
		}
		return trustapi.Ticket{}, fmt.Errorf("save ticket: %w", err)
	}
	return toAPITicket(t), nil
}

// AdminResolveTicket records the verdict and what was done about it. Recording is all it does:
// taking a listing down, suspending an account and granting a refund are calls to the modules that
// own them, so the decision and its effects each stay where they can be audited.
//
// A refund dispute is the one kind whose money is decided elsewhere — order's verdict route — and
// resolving that ticket here without it would close the case while the escrow still waits.
func (s *Service) AdminResolveTicket(ctx context.Context, req trustapi.ResolveTicketRequest) (trustapi.Ticket, error) {
	if err := s.v.Struct(req); err != nil {
		return trustapi.Ticket{}, err
	}
	if err := s.requireModerator(ctx, req.ActorID); err != nil {
		return trustapi.Ticket{}, err
	}
	t, err := s.repo.FindTicket(ctx, req.ID.Int64())
	if err != nil {
		return trustapi.Ticket{}, fmt.Errorf("find ticket: %w", err)
	}
	// A refund dispute is decided by moving the money, not by writing here: order's verdict route
	// is what pays somebody, and it closes this ticket on its way out. Answering 200 to a hand
	// resolution would leave a case marked settled with the escrow untouched.
	if t.Kind == domain.KindRefundDispute {
		return trustapi.Ticket{}, domain.ErrTicketDecidedElsewhere
	}
	if err := t.Resolve(req.ActorID.Int64(), req.ActionTaken, req.Note); err != nil {
		return trustapi.Ticket{}, err
	}
	if err := s.repo.SaveTicket(ctx, t, openStatuses); err != nil {
		return trustapi.Ticket{}, err
	}
	return toAPITicket(t), nil
}

// requireTarget refuses a ticket about something that does not exist, and — for an order or a
// refund — something that is not the caller's. Each kind is asked of the module that owns it: a
// resource id only resolves inside its module, and so does everything else.
func (s *Service) requireTarget(ctx context.Context, actorID id.ID[id.Account], refType string, refID int64) error {
	var err error
	switch refType {
	case domain.RefListing:
		_, err = s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
			ID: id.Of[id.Listing](refID), ViewerID: actorID,
		})
	case domain.RefAccount:
		_, err = s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
			ID: id.Of[id.Account](refID),
		})
	case domain.RefMessage:
		_, err = s.chat.GetMessage(ctx, chatapi.GetMessageRequest{
			ActorID: actorID, ID: id.Of[id.Message](refID),
		})
	case domain.RefReview:
		_, err = s.repo.FindReview(ctx, refID)
	case domain.RefReviewReply:
		_, err = s.repo.FindReply(ctx, refID)
	case domain.RefOrder:
		_, err = s.orders.GetOrder(ctx, orderapi.OrderRequest{
			ActorID: actorID, ID: id.Of[id.Order](refID),
		})
	case domain.RefRefund:
		_, err = s.orders.GetRefund(ctx, orderapi.RefundRequest{
			ActorID: actorID, ID: id.Of[id.Refund](refID),
		})
	}
	if err == nil {
		return nil
	}
	// Only a not-found means the target is not there. Telling a reporter their target does not
	// exist because chat was briefly unreachable makes them stop reporting it.
	if status, _, _, ok := errx.Decompose(err); ok && status == http.StatusNotFound {
		return domain.ErrTicketTargetMissing
	}
	return fmt.Errorf("read ticket target: %w", err)
}

// target is what the ticket is about, fetched from the module that owns it. Nil for a kind that is
// about nothing in particular, and nil best-effort when the owner no longer has it: a listing
// already taken down still leaves a decision somebody has to record.
func (s *Service) target(ctx context.Context, actorID id.ID[id.Account], t domain.Ticket) map[string]any {
	if t.RefType == nil || t.RefID == nil {
		return nil
	}
	switch *t.RefType {
	case domain.RefListing:
		listing, err := s.catalog.GetListing(ctx, catalogapi.GetListingRequest{
			ID: id.Of[id.Listing](*t.RefID), ViewerID: actorID,
		})
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": listing.ID, "name": listing.Name, "status": listing.Status,
			"seller": listing.Seller,
		}
	case domain.RefAccount:
		account, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
			ID: id.Of[id.Account](*t.RefID),
		})
		if err != nil {
			return nil
		}
		return map[string]any{"id": account.ID, "name": account.Name, "created_at": account.CreatedAt}
	case domain.RefOrder:
		// Both kinds that point at a sale — `refund-dispute` and `order-issue` — landed here
		// with no case at all, so `target` was null for exactly the tickets whose decision
		// needs the most context. The refund travels with the order because the verdict route
		// names the refund while the ticket names the order, and nothing else bridged the two.
		orderCase, err := s.orders.GetOrderCase(ctx, orderapi.OrderRequest{
			ActorID: actorID, ID: id.Of[id.Order](*t.RefID),
		})
		if err != nil {
			return nil
		}
		out := map[string]any{
			"id": orderCase.Order.ID, "state": orderCase.Order.State,
			"total": orderCase.Order.Total, "currency": orderCase.Order.Currency,
			"buyer": orderCase.Order.Buyer, "seller": orderCase.Order.Seller,
			"created_at": orderCase.Order.CreatedAt,
		}
		if r := orderCase.Refund; r != nil {
			// `returned_at` is what decides whether a verdict for the buyer books the return
			// leg or pays out, so staff have to see it before they press.
			out["refund"] = map[string]any{
				"id": r.ID, "status": r.Status, "reason": r.Reason,
				"returned_at": r.ReturnedAt, "deadline_at": r.DeadlineAt,
			}
		}
		return out
	case domain.RefMessage:
		message, err := s.chat.GetMessage(ctx, chatapi.GetMessageRequest{
			ActorID: actorID, ID: id.Of[id.Message](*t.RefID),
		})
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": message.ID, "sender_id": message.SenderID, "body": message.Body,
			"created_at": message.CreatedAt,
		}
	case domain.RefReview:
		review, err := s.repo.FindReview(ctx, *t.RefID)
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": id.Of[id.Review](review.ID), "listing_id": id.Of[id.Listing](review.ListingID),
			"author_id": id.Of[id.Account](review.AuthorID), "rating": review.Rating,
			"body": review.Body, "created_at": review.CreatedAt,
		}
	case domain.RefReviewReply:
		reply, err := s.repo.FindReply(ctx, *t.RefID)
		if err != nil {
			return nil
		}
		return map[string]any{
			"id": id.Of[id.ReviewReply](reply.ID), "review_id": id.Of[id.Review](reply.ReviewID),
			"author_id": id.Of[id.Account](reply.AuthorID), "body": reply.Body,
			"created_at": reply.CreatedAt,
		}
	}
	return nil
}

// adminViews renders a page of the queue: the names, the patterns, each resolved once for the whole
// page rather than per row. A page of twenty used to cost about sixty round trips — two account reads
// and a count per entry — which is the same N+1 the review page already avoids.
func (s *Service) adminViews(ctx context.Context, actorID id.ID[id.Account], rows []domain.Ticket,
	withTarget bool) ([]trustapi.AdminTicket, error) {
	if len(rows) == 0 {
		return []trustapi.AdminTicket{}, nil
	}
	accountIDs := make([]int64, 0, 3*len(rows))
	targets := make([]port.TicketTarget, 0, len(rows))
	for _, row := range rows {
		accountIDs = append(accountIDs, row.RequesterID)
		if row.AssigneeID != nil {
			accountIDs = append(accountIDs, *row.AssigneeID)
		}
		if row.ResolvedByID != nil {
			accountIDs = append(accountIDs, *row.ResolvedByID)
		}
		if row.RefType != nil && row.RefID != nil {
			targets = append(targets, port.TicketTarget{RefType: *row.RefType, RefID: *row.RefID})
		}
	}
	counts, err := s.repo.CountOpenAgainst(ctx, targets)
	if err != nil {
		return nil, fmt.Errorf("count open tickets: %w", err)
	}
	names := s.summaries(ctx, accountIDs)

	out := make([]trustapi.AdminTicket, 0, len(rows))
	for _, row := range rows {
		entry := trustapi.AdminTicket{
			Ticket:    toAPITicket(row),
			Requester: names[row.RequesterID],
		}
		if row.RefType != nil && row.RefID != nil {
			entry.OpenTicketsAgainstTarget = counts[port.TicketTarget{
				RefType: *row.RefType, RefID: *row.RefID,
			}]
		}
		// The assignee is here and never on the requester's own view: staff need to know whose case
		// it is, and the requester is answered by the desk.
		if row.AssigneeID != nil {
			assignee := names[*row.AssigneeID]
			entry.Assignee = &assignee
		}
		if row.ResolvedByID != nil {
			resolver := names[*row.ResolvedByID]
			entry.ResolvedBy = &resolver
		}
		if withTarget {
			entry.Target = s.target(ctx, actorID, row)
		}
		out = append(out, entry)
	}
	return out, nil
}

// ticketKey is the pair the queue and the requester's own history both order by.
func ticketKey(t domain.Ticket) (int64, int64) { return t.CreatedAt.UnixNano(), t.ID }

// toAPITicket encodes the polymorphic target with its own kind's prefix, so a client never sees a
// raw row id and cannot hand a listing id back where a refund id belongs.
func toAPITicket(t domain.Ticket) trustapi.Ticket {
	out := trustapi.Ticket{
		ID:             id.Of[id.Ticket](t.ID),
		Kind:           t.Kind,
		Subject:        t.Subject,
		RefType:        t.RefType,
		Reason:         t.Reason,
		Status:         t.Status,
		ActionTaken:    t.ActionTaken,
		ResolvedAt:     t.ResolvedAt,
		ResolutionNote: t.ResolutionNote,
		CreatedAt:      t.CreatedAt,
	}
	if t.RefType != nil && t.RefID != nil {
		if prefix, ok := prefixFor(*t.RefType); ok {
			out.RefID = new(id.FormatOpaque(prefix, *t.RefID))
		}
	}
	if t.ConversationID != nil {
		out.ConversationID = new(id.Of[id.Conversation](*t.ConversationID))
	}
	return out
}

// statusFilter turns an optional single status into the set the repository takes. Absent
// means every status, which is what a reporter's own history shows.
func statusFilter(status string) []string {
	if status == "" {
		return nil
	}
	return []string{status}
}

// RecordRefundVerdict closes every ticket a refund dispute opened. Order decided it — the money is
// its business — and this is the half a requester reads: the verdict lands on their ticket and as a
// message in the thread they raised it in.
//
// Every one, not the newest: both parties may escalate the same refund and each gets their own
// ticket, so one verdict answers however many exist. A ticket left open on a decided refund can
// never be closed at all, because resolving that kind by hand is refused.
//
// Idempotent and quiet about a case it does not know: the lookup only ever returns unresolved
// tickets, so a redelivered verdict — or one for a refund escalated before this module existed —
// finds nothing to do rather than retrying for ever.
func (s *Service) RecordRefundVerdict(ctx context.Context, req trustapi.RecordRefundVerdictRequest) error {
	if err := s.v.Struct(req); err != nil {
		return err
	}
	// Against the order, because that is what a refund-dispute ticket names. Both parties may have
	// raised one about the same sale and the index holds one open ticket per requester, so a lookup
	// that answered a single row would leave the other open for ever — and unclosable, since this
	// kind refuses a hand resolution.
	open, err := s.repo.OpenTicketsAgainst(ctx, domain.RefOrder, req.OrderID.Int64())
	if err != nil {
		return fmt.Errorf("find refund tickets: %w", err)
	}
	action := domain.ActionRefundRefused
	if req.BuyerWins {
		action = domain.ActionRefundGranted
	}
	for _, t := range open {
		if err := t.Resolve(req.ModeratorID.Int64(), action, req.Note); err != nil {
			return err
		}
		if err := s.repo.SaveTicket(ctx, t, openStatuses); err != nil {
			return err
		}
		// Best-effort: the verdict is recorded and the money has moved, so a chat that is down must
		// not make order's published fact redeliver into tickets that are already closed.
		if _, err := s.chat.PostTicketMessage(ctx, chatapi.PostTicketMessageRequest{
			RequesterID: id.Of[id.Account](t.RequesterID),
			TicketID:    id.Of[id.Ticket](t.ID),
			Body:        req.Note,
			Card:        map[string]any{"refund_id": req.RefundID.String(), "action_taken": action},
		}); err != nil {
			s.log.Error("post refund verdict message", "ticket_id", t.ID, "err", err)
		}
	}
	return nil
}
