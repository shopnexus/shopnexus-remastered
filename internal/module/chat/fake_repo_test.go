package chat_test

import (
	"context"
	"slices"
	"time"

	"shopnexus/internal/module/chat/domain"
	"shopnexus/internal/module/chat/port"
	"shopnexus/internal/module/common"
)

// fakeRepo is an in-memory port.Repository. It keeps the rules the schema holds — one
// thread per ordered pair, a read mark that only moves forward, a redacted row that stays
// — because those are what the service's behaviour rests on.
type fakeRepo struct {
	nextID   int64
	threads  map[int64]domain.Conversation
	messages []domain.Message
	// resources is this module's own resource table: an id absent from it names no
	// confirmed upload.
	resources map[int64]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{threads: map[int64]domain.Conversation{}, resources: map[int64]bool{}}
}

func (f *fakeRepo) id() int64 {
	f.nextID++
	return f.nextID
}

var _ port.Repository = (*fakeRepo)(nil)

// EnsureConversation is an upsert on the ordered pair: two people who start writing at
// the same moment get one thread, not two.
func (f *fakeRepo) EnsureConversation(_ context.Context, one, other int64) (domain.Conversation, error) {
	c, err := domain.NewConversation(one, other)
	if err != nil {
		return domain.Conversation{}, err
	}
	for _, stored := range f.threads {
		if stored.AccountAID == c.AccountAID && stored.AccountBID == c.AccountBID {
			return stored, nil
		}
	}
	c.ID = f.id()
	c.CreatedAt = time.Now()
	f.threads[c.ID] = c
	return c, nil
}

func (f *fakeRepo) FindConversation(_ context.Context, conversationID int64) (domain.Conversation, error) {
	c, ok := f.threads[conversationID]
	if !ok {
		return domain.Conversation{}, domain.ErrConversationNotFound
	}
	return c, nil
}

func (f *fakeRepo) ListConversations(_ context.Context, filter port.InboxFilter) ([]domain.Conversation, error) {
	var matched []domain.Conversation
	for _, c := range f.threads {
		if !c.Involves(filter.AccountID) {
			continue
		}
		if !filter.Before.IsZero() && !c.LastMessageAt.Before(filter.Before) {
			continue
		}
		matched = append(matched, c)
	}
	slices.SortFunc(matched, func(a, b domain.Conversation) int {
		if a.LastMessageAt.Equal(b.LastMessageAt) {
			return int(b.ID - a.ID)
		}
		return b.LastMessageAt.Compare(a.LastMessageAt)
	})
	return matched[:min(filter.Limit, len(matched))], nil
}

// SaveConversation takes the later mark, as GREATEST does: a replayed request must not
// un-read a thread.
func (f *fakeRepo) SaveConversation(_ context.Context, c domain.Conversation) error {
	stored, ok := f.threads[c.ID]
	if !ok {
		return domain.ErrConversationNotFound
	}
	stored.AccountAReadAt = later(stored.AccountAReadAt, c.AccountAReadAt)
	stored.AccountBReadAt = later(stored.AccountBReadAt, c.AccountBReadAt)
	f.threads[c.ID] = stored
	return nil
}

func later(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil || !b.After(*a) {
		return a
	}
	return b
}

// InsertMessage also moves the thread's last_message_at, as the transaction does: an
// inbox ordered by a timestamp the message did not set would sort wrongly.
func (f *fakeRepo) InsertMessage(_ context.Context, m *domain.Message) error {
	m.ID = f.id()
	m.CreatedAt = time.Now()
	f.messages = append(f.messages, *m)
	if thread, ok := f.threads[m.ConversationID]; ok {
		thread.LastMessageAt = m.CreatedAt
		f.threads[m.ConversationID] = thread
	}
	return nil
}

func (f *fakeRepo) FindMessage(_ context.Context, messageID int64) (domain.Message, error) {
	for _, m := range f.messages {
		if m.ID == messageID {
			return m, nil
		}
	}
	return domain.Message{}, domain.ErrMessageNotFound
}

func (f *fakeRepo) SaveMessage(_ context.Context, m domain.Message) error {
	for i, stored := range f.messages {
		if stored.ID == m.ID {
			f.messages[i] = m
			return nil
		}
	}
	return domain.ErrMessageNotFound
}

func (f *fakeRepo) ListMessages(_ context.Context, filter port.HistoryFilter) ([]domain.Message, error) {
	var matched []domain.Message
	for _, m := range f.messages {
		if m.ConversationID != filter.ConversationID {
			continue
		}
		if !filter.Before.IsZero() && !m.CreatedAt.Before(filter.Before) {
			continue
		}
		matched = append(matched, m)
	}
	slices.SortFunc(matched, func(a, b domain.Message) int { return int(b.ID - a.ID) })
	return matched[:min(filter.Limit, len(matched))], nil
}

func (f *fakeRepo) LastMessages(_ context.Context, conversationIDs []int64) (map[int64]domain.Message, error) {
	out := map[int64]domain.Message{}
	for _, m := range f.messages {
		if !slices.Contains(conversationIDs, m.ConversationID) {
			continue
		}
		if current, ok := out[m.ConversationID]; !ok || m.ID > current.ID {
			out[m.ConversationID] = m
		}
	}
	return out, nil
}

// UnreadCounts is the counterparty's live messages after the caller's own mark.
func (f *fakeRepo) UnreadCounts(_ context.Context, accountID int64, conversationIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		thread, ok := f.threads[conversationID]
		if !ok {
			continue
		}
		out[conversationID] = f.unreadIn(thread, accountID)
	}
	return out, nil
}

func (f *fakeRepo) UnreadTotal(_ context.Context, accountID int64) (int64, int64, error) {
	var total, threads int64
	for _, thread := range f.threads {
		if !thread.Involves(accountID) {
			continue
		}
		if unread := f.unreadIn(thread, accountID); unread > 0 {
			total += unread
			threads++
		}
	}
	return total, threads, nil
}

func (f *fakeRepo) unreadIn(thread domain.Conversation, accountID int64) int64 {
	mark := thread.ReadMark(accountID)
	var unread int64
	for _, m := range f.messages {
		// A system message has a zero sender, so it is nobody's own and counts for both
		// sides — as `sender_id IS DISTINCT FROM` does in the statement.
		if m.ConversationID != thread.ID || !m.IsLive() {
			continue
		}
		if m.SenderID != 0 && m.SenderID == accountID {
			continue
		}
		if mark == nil || m.CreatedAt.After(*mark) {
			unread++
		}
	}
	return unread
}

func (f *fakeRepo) FindResources(_ context.Context, ids []int64) ([]common.Resource, error) {
	out := make([]common.Resource, 0, len(ids))
	for _, key := range ids {
		if f.resources[key] {
			out = append(out, common.Resource{ID: key, Mime: "image/jpeg"})
		}
	}
	return out, nil
}
