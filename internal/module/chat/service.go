// Package chat implements chatapi.Service — one thread per pair of accounts, its
// messages, and the read marks behind an unread badge.
package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	accountapi "shopnexus/internal/module/account/api"
	chatapi "shopnexus/internal/module/chat/api"
	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/module/common"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/validation"
)

type Service struct {
	repo port.Repository
	// accounts answers what a counterparty is called, which is what an inbox row shows.
	accounts accountapi.Service
	// uploads is this module's own resource table plus the object store. A message
	// attachment belongs to the module that took the upload, and resolving one through here
	// is what puts a live link on it rather than an id nothing can render.
	uploads common.Uploads
	v       *validator.Validate
	log     *slog.Logger
}

func NewService(repo port.Repository, accounts accountapi.Service, uploads common.Uploads, v *validator.Validate, log *slog.Logger) *Service {
	return &Service{repo: repo, accounts: accounts, uploads: uploads, v: v, log: log}
}

var _ chatapi.Service = (*Service)(nil)

// CreateUpload reserves a row and a signed slot for a message attachment. The client PUTs
// the bytes at the store and confirms; until then the resource resolves to nothing, so a
// half-finished upload cannot be attached to a message.
func (s *Service) CreateUpload(ctx context.Context, req chatapi.CreateUploadRequest) (chatapi.UploadSlot, error) {
	if err := s.v.Struct(req); err != nil {
		return chatapi.UploadSlot{}, err
	}
	slot, err := s.uploads.Presign(ctx, req.ActorID.Int64(), "message", common.UploadRequest{
		Filename: req.Filename, Mime: req.Mime, Size: req.Size,
	})
	if err != nil {
		return chatapi.UploadSlot{}, err
	}
	return chatapi.UploadSlot{
		ResourceID: id.Of[id.Resource](slot.ResourceID),
		URL:        slot.URL,
		Headers:    slot.Headers,
		ExpiresAt:  slot.ExpiresAt,
	}, nil
}

// ConfirmUpload makes the attachment real, with the size the store reports rather than the
// one the client declared. Scoped to the uploader: a resource id is guessable, and
// confirming somebody else's slot would be claiming their upload.
func (s *Service) ConfirmUpload(ctx context.Context, req chatapi.ConfirmUploadRequest) (common.ResourceDTO, error) {
	if err := s.v.Struct(req); err != nil {
		return common.ResourceDTO{}, err
	}
	res, err := s.uploads.Confirm(ctx, req.ActorID.Int64(), req.ID.Int64())
	if err != nil {
		return common.ResourceDTO{}, err
	}
	return res.ToDTO(), nil
}

// ListConversations is the inbox, latest activity first. Two extra queries for the whole
// page — the last message and the unread counts — rather than per row.
func (s *Service) ListConversations(ctx context.Context, req chatapi.ListConversationsRequest) (chatapi.ConversationPage, error) {
	before, beforeID, err := parseCursor(req.Cursor)
	if err != nil {
		return chatapi.ConversationPage{}, err
	}
	// One more than asked, so "is there another page" is answered without a count.
	threads, err := s.repo.ListConversations(ctx, port.InboxFilter{
		AccountID: req.ActorID.Int64(),
		Before:    before,
		BeforeID:  beforeID,
		Limit:     req.Limit + 1,
	})
	if err != nil {
		return chatapi.ConversationPage{}, fmt.Errorf("list conversations: %w", err)
	}
	hasMore := len(threads) > req.Limit
	if hasMore {
		threads = threads[:req.Limit]
	}
	out, err := s.inboxRows(ctx, req.ActorID.Int64(), threads)
	if err != nil {
		return chatapi.ConversationPage{}, err
	}
	page := chatapi.ConversationPage{Data: out, Meta: chatapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(threads) > 0 {
		last := threads[len(threads)-1]
		page.Meta.NextCursor = formatCursor(last.LastMessageAt, last.ID)
	}
	return page, nil
}

// StartConversation opens the thread with one account, or answers the one that already
// exists — there is one per pair, so the route is idempotent by construction.
func (s *Service) StartConversation(ctx context.Context, req chatapi.StartConversationRequest) (chatapi.Conversation, error) {
	thread, err := s.repo.EnsureConversation(ctx, req.ActorID.Int64(), req.AccountID.Int64())
	if err != nil {
		return chatapi.Conversation{}, fmt.Errorf("ensure conversation: %w", err)
	}
	return s.inboxRow(ctx, req.ActorID.Int64(), thread, nil, 0)
}

func (s *Service) GetConversation(ctx context.Context, req chatapi.GetConversationRequest) (chatapi.Conversation, error) {
	thread, err := s.participant(ctx, req.ActorID, req.ID)
	if err != nil {
		return chatapi.Conversation{}, err
	}
	rows, err := s.inboxRows(ctx, req.ActorID.Int64(), []domain.Conversation{thread})
	if err != nil {
		return chatapi.Conversation{}, err
	}
	return rows[0], nil
}

// GetUnreadCount is the badge. Two numbers, because "12 unread" and "in 3 threads" are
// different things a client shows in different places.
func (s *Service) GetUnreadCount(ctx context.Context, req chatapi.UnreadCountRequest) (chatapi.UnreadCount, error) {
	total, threads, err := s.repo.UnreadTotal(ctx, req.ActorID.Int64())
	if err != nil {
		return chatapi.UnreadCount{}, fmt.Errorf("read unread total: %w", err)
	}
	return chatapi.UnreadCount{Unread: total, Conversations: threads}, nil
}

// ListMessages pages a thread newest first. A thread the caller is not in is not found
// rather than forbidden — it is not theirs to know about.
func (s *Service) ListMessages(ctx context.Context, req chatapi.ListMessagesRequest) (chatapi.MessagePage, error) {
	if _, err := s.participant(ctx, req.ActorID, req.ID); err != nil {
		return chatapi.MessagePage{}, err
	}
	before, beforeID, err := parseCursor(req.Cursor)
	if err != nil {
		return chatapi.MessagePage{}, err
	}
	messages, err := s.repo.ListMessages(ctx, port.HistoryFilter{
		ConversationID: req.ID.Int64(),
		Before:         before,
		BeforeID:       beforeID,
		Limit:          req.Limit + 1,
	})
	if err != nil {
		return chatapi.MessagePage{}, fmt.Errorf("list messages: %w", err)
	}
	hasMore := len(messages) > req.Limit
	if hasMore {
		messages = messages[:req.Limit]
	}
	out, err := s.toAPIMessages(ctx, messages)
	if err != nil {
		return chatapi.MessagePage{}, err
	}
	page := chatapi.MessagePage{Data: out, Meta: chatapi.CursorInfo{HasMore: hasMore}}
	if hasMore && len(messages) > 0 {
		last := messages[len(messages)-1]
		page.Meta.NextCursor = formatCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// SendMessage appends to a thread the caller is in. The attachments have to be this
// module's own confirmed uploads: a message pointing at nothing is a photo that never
// renders.
func (s *Service) SendMessage(ctx context.Context, req chatapi.SendMessageRequest) (chatapi.Message, error) {
	if _, err := s.participant(ctx, req.ActorID, req.ConversationID); err != nil {
		return chatapi.Message{}, err
	}
	attachments := resourceKeys(req.Attachments)
	if err := s.requireResources(ctx, attachments); err != nil {
		return chatapi.Message{}, err
	}
	m, err := domain.NewMessage(req.ConversationID.Int64(), req.ActorID.Int64(), req.Body,
		attachments, req.Refs)
	if err != nil {
		return chatapi.Message{}, err
	}
	if err := s.repo.InsertMessage(ctx, &m); err != nil {
		return chatapi.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return s.toAPIMessage(ctx, m)
}

// MarkConversationRead moves the caller's own mark. Absent means "everything so far",
// which is what opening a thread does.
func (s *Service) MarkConversationRead(ctx context.Context, req chatapi.MarkConversationReadRequest) (chatapi.Conversation, error) {
	thread, err := s.participant(ctx, req.ActorID, req.ID)
	if err != nil {
		return chatapi.Conversation{}, err
	}
	at := time.Now()
	if req.Before != nil {
		at = *req.Before
	}
	if err := thread.MarkRead(req.ActorID.Int64(), at); err != nil {
		return chatapi.Conversation{}, err
	}
	if err := s.repo.SaveConversation(ctx, thread); err != nil {
		return chatapi.Conversation{}, fmt.Errorf("save conversation: %w", err)
	}
	rows, err := s.inboxRows(ctx, req.ActorID.Int64(), []domain.Conversation{thread})
	if err != nil {
		return chatapi.Conversation{}, err
	}
	return rows[0], nil
}

// UpdateMessage rewrites a body. Only the sender's own, and never a system message or one
// already unsent.
func (s *Service) UpdateMessage(ctx context.Context, req chatapi.UpdateMessageRequest) (chatapi.Message, error) {
	m, err := s.repo.FindMessageAt(ctx, req.ID.Int64(), req.CreatedAt)
	if err != nil {
		return chatapi.Message{}, fmt.Errorf("find message: %w", err)
	}
	if err := m.Edit(req.ActorID.Int64(), req.Body); err != nil {
		return chatapi.Message{}, err
	}
	if err := s.repo.SaveMessage(ctx, m); err != nil {
		return chatapi.Message{}, fmt.Errorf("save message: %w", err)
	}
	return s.toAPIMessage(ctx, m)
}

// RedactMessage is unsending: the content goes, the row stays so the thread has no
// unexplained gaps.
func (s *Service) RedactMessage(ctx context.Context, req chatapi.RedactMessageRequest) error {
	m, err := s.repo.FindMessageAt(ctx, req.ID.Int64(), req.CreatedAt)
	if err != nil {
		return fmt.Errorf("find message: %w", err)
	}
	if err := m.Redact(req.ActorID.Int64(), false); err != nil {
		return err
	}
	if err := s.repo.SaveMessage(ctx, m); err != nil {
		return fmt.Errorf("save message: %w", err)
	}
	return nil
}

// PostSystemMessage is another module speaking into the pair's thread — an offer card, an
// order update. It opens the thread if they have never spoken, so a caller never has to
// know whether one exists.
func (s *Service) PostSystemMessage(ctx context.Context, req chatapi.PostSystemMessageRequest) (chatapi.Message, error) {
	if err := validation.AsError(s.v.Struct(req)); err != nil {
		return chatapi.Message{}, err
	}
	thread, err := s.repo.EnsureConversation(ctx, req.AccountAID.Int64(), req.AccountBID.Int64())
	if err != nil {
		return chatapi.Message{}, fmt.Errorf("ensure conversation: %w", err)
	}
	m, err := domain.NewSystemMessage(thread.ID, req.Body, req.Card)
	if err != nil {
		return chatapi.Message{}, err
	}
	if err := s.repo.InsertMessage(ctx, &m); err != nil {
		return chatapi.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return s.toAPIMessage(ctx, m)
}

// GetMessage reads one message. A participant sees their own thread's; a moderator sees any,
// because a report about a message is judged on the message itself. Not found rather than
// forbidden for everyone else: a thread they are not part of is not theirs to know about.
func (s *Service) GetMessage(ctx context.Context, req chatapi.GetMessageRequest) (chatapi.Message, error) {
	if err := s.v.Struct(req); err != nil {
		return chatapi.Message{}, err
	}
	m, err := s.repo.FindMessage(ctx, req.ID.Int64())
	if err != nil {
		return chatapi.Message{}, fmt.Errorf("find message: %w", err)
	}
	thread, err := s.repo.FindConversation(ctx, m.ConversationID)
	if err != nil {
		return chatapi.Message{}, fmt.Errorf("find conversation: %w", err)
	}
	if !thread.Involves(req.ActorID.Int64()) && !s.isModerator(ctx, req.ActorID) {
		return chatapi.Message{}, domain.ErrMessageNotFound
	}
	return s.toAPIMessage(ctx, m)
}

// isModerator asks the account module for the caller's role: it is a row in that module's
// table. An admin passes every moderator check.
func (s *Service) isModerator(ctx context.Context, actorID id.ID[id.Account]) bool {
	me, err := s.accounts.GetMe(ctx, accountapi.GetMeRequest{ActorID: actorID})
	if err != nil {
		return false
	}
	return me.Role == accountapi.RoleModerator || me.Role == accountapi.RoleAdmin
}

// participant reads a thread the caller is in. Not found rather than forbidden: a thread
// they are not part of is not theirs to know about.
func (s *Service) participant(ctx context.Context, actorID id.ID[id.Account], conversationID id.ID[id.Conversation]) (domain.Conversation, error) {
	thread, err := s.repo.FindConversation(ctx, conversationID.Int64())
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("find conversation: %w", err)
	}
	if !thread.Involves(actorID.Int64()) {
		return domain.Conversation{}, domain.ErrConversationNotFound
	}
	return thread, nil
}

// inboxRows fills a page: the last message and the unread count for every thread at once,
// and the counterparty's name per row because the account module has no batch read.
func (s *Service) inboxRows(ctx context.Context, viewerID int64, threads []domain.Conversation) ([]chatapi.Conversation, error) {
	ids := make([]int64, 0, len(threads))
	for _, t := range threads {
		ids = append(ids, t.ID)
	}
	lastMessages, err := s.repo.LastMessages(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read last messages: %w", err)
	}
	unread, err := s.repo.UnreadCounts(ctx, viewerID, ids)
	if err != nil {
		return nil, fmt.Errorf("read unread counts: %w", err)
	}
	out := make([]chatapi.Conversation, 0, len(threads))
	for _, t := range threads {
		var last *domain.Message
		if m, ok := lastMessages[t.ID]; ok {
			last = &m
		}
		row, err := s.inboxRow(ctx, viewerID, t, last, unread[t.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) inboxRow(ctx context.Context, viewerID int64, t domain.Conversation, last *domain.Message, unread int64) (chatapi.Conversation, error) {
	counterparty, err := s.accounts.GetPublicAccount(ctx, accountapi.GetPublicAccountRequest{
		ID: id.Of[id.Account](t.Counterparty(viewerID)),
	})
	if err != nil {
		return chatapi.Conversation{}, fmt.Errorf("read counterparty: %w", err)
	}
	row := chatapi.Conversation{
		ID: id.Of[id.Conversation](t.ID),
		Counterparty: accountapi.AccountSummary{
			ID: counterparty.ID, Name: counterparty.Name, Avatar: counterparty.Avatar,
		},
		LastMessageAt:      t.LastMessageAt,
		Unread:             unread,
		ReadAt:             t.ReadMark(viewerID),
		CounterpartyReadAt: t.CounterpartyReadMark(viewerID),
		CreatedAt:          t.CreatedAt,
	}
	if last != nil {
		message, err := s.toAPIMessage(ctx, *last)
		if err != nil {
			return chatapi.Conversation{}, err
		}
		row.LastMessage = &message
	}
	return row, nil
}

func (s *Service) toAPIMessages(ctx context.Context, messages []domain.Message) ([]chatapi.Message, error) {
	// One resource read for the whole page rather than one per message.
	var keys []int64
	for _, m := range messages {
		keys = append(keys, m.Attachments...)
	}
	images, err := s.resources(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make([]chatapi.Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, buildMessage(m, images))
	}
	return out, nil
}

func (s *Service) toAPIMessage(ctx context.Context, m domain.Message) (chatapi.Message, error) {
	images, err := s.resources(ctx, m.Attachments)
	if err != nil {
		return chatapi.Message{}, err
	}
	return buildMessage(m, images), nil
}

func buildMessage(m domain.Message, images map[int64]common.ResourceDTO) chatapi.Message {
	out := chatapi.Message{
		ID:             id.Of[id.Message](m.ID),
		ConversationID: id.Of[id.Conversation](m.ConversationID),
		Type:           m.Type,
		Body:           m.Body,
		Images:         pick(images, m.Attachments),
		Refs:           m.Refs,
		Card:           m.Card,
		CreatedAt:      m.CreatedAt,
		EditedAt:       m.EditedAt,
		DeletedAt:      m.DeletedAt,
	}
	// Null on a system message: that one is the backend's word, not a person's.
	if m.SenderID != 0 {
		out.SenderID = new(id.Of[id.Account](m.SenderID))
	}
	return out
}

// requireResources refuses an attachment that names no confirmed upload of this module's.
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
	if len(keys) == 0 {
		return map[int64]common.ResourceDTO{}, nil
	}
	return s.uploads.Resolve(ctx, keys)
}

// pick keeps the sender's order, which is the only thing the array encodes.
func pick(found map[int64]common.ResourceDTO, keys []int64) []common.ResourceDTO {
	out := make([]common.ResourceDTO, 0, len(keys))
	for _, key := range keys {
		if res, ok := found[key]; ok {
			out = append(out, res)
		}
	}
	return out
}

func resourceKeys(ids []id.ID[id.Resource]) []int64 {
	out := make([]int64, 0, len(ids))
	for _, rid := range ids {
		out = append(out, rid.Int64())
	}
	return out
}

// The cursor is the (timestamp, id) a page ended at — opaque to a client, and a tuple
// because CURRENT_TIMESTAMP is transaction-scoped: two rows written in the same
// transaction share a timestamp exactly, and a bare-timestamp cursor would then drop
// whichever of them page N did not return. Ordering by the tuple, matching every
// ORDER BY's own tiebreak, is what keeps a boundary tie from skipping a row.
func formatCursor(at time.Time, id int64) string {
	return strconv.FormatInt(at.UnixNano(), 10) + "_" + strconv.FormatInt(id, 10)
}

func parseCursor(cursor string) (time.Time, int64, error) {
	if cursor == "" {
		return time.Time{}, 0, nil
	}
	nanos, rest, ok := strings.Cut(cursor, "_")
	if !ok {
		return time.Time{}, 0, domain.ErrCursorInvalid
	}
	at, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return time.Time{}, 0, domain.ErrCursorInvalid
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return time.Time{}, 0, domain.ErrCursorInvalid
	}
	return time.Unix(0, at), id, nil
}
