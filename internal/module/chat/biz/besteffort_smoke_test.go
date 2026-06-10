package chatbiz_test

import (
	"context"
	"net/http/httptest"
	"testing"

	chatbiz "shopnexus-server/internal/module/chat/biz"
	chatdb "shopnexus-server/internal/module/chat/db/sqlc"
	"shopnexus-server/internal/shared/besteffort"
	errors "shopnexus-server/internal/shared/errors"
	"shopnexus-server/internal/shared/paginate"

	"github.com/google/uuid"
	restate "github.com/restatedev/sdk-go"
)

// sentinelID triggers the error path in fakeChatBiz.GetConversation.
var sentinelID = uuid.MustParse("00000000-0000-0000-0000-0000000000ff")

// fakeChatBiz records the last GetConversation arg and returns a canned conversation,
// or a coded domain error when sentinelID is passed.
type fakeChatBiz struct {
	gotID  uuid.UUID
	canned chatdb.ChatConversation
}

func (f *fakeChatBiz) GetConversation(_ context.Context, id uuid.UUID) (chatdb.ChatConversation, error) {
	f.gotID = id
	if id == sentinelID {
		return chatdb.ChatConversation{}, errors.NewError(409, "smoke_conflict", "boom")
	}
	return f.canned, nil
}

// remaining ChatBiz methods are unused; zero values satisfy the interface.
// Commands take restate.Context (durable); queries take context.Context.
func (f *fakeChatBiz) CreateConversation(
	restate.Context, chatbiz.CreateConversationParams,
) (chatdb.ChatConversation, error) {
	return chatdb.ChatConversation{}, nil
}

func (f *fakeChatBiz) ListConversation(
	context.Context, chatbiz.ListConversationParams,
) (paginate.PaginateResult[chatdb.ChatConversation], error) {
	return paginate.PaginateResult[chatdb.ChatConversation]{}, nil
}

func (f *fakeChatBiz) SendMessage(restate.Context, chatbiz.SendMessageParams) (chatdb.ChatMessage, error) {
	return chatdb.ChatMessage{}, nil
}

func (f *fakeChatBiz) ListMessage(
	context.Context, chatbiz.ListMessageParams,
) (paginate.PaginateResult[chatdb.ChatMessage], error) {
	return paginate.PaginateResult[chatdb.ChatMessage]{}, nil
}

func (f *fakeChatBiz) MarkRead(restate.Context, chatbiz.MarkReadParams) error { return nil }

func newFake() *fakeChatBiz {
	return &fakeChatBiz{
		canned: chatdb.ChatConversation{ID: uuid.New(), BuyerID: uuid.New(), SellerID: uuid.New()},
	}
}

// In-process flat query delegates straight to the biz, no network.
func TestSmokeFlatQueryInProcess(t *testing.T) {
	t.Parallel()
	fake := newFake()
	c := chatbiz.NewChatBizClientInProcess("http://unused", fake)

	out, err := c.GetConversation(context.Background(), fake.canned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotID != fake.canned.ID {
		t.Fatalf("biz not reached: gotID=%v want=%v", fake.gotID, fake.canned.ID)
	}
	if out.ID != fake.canned.ID {
		t.Errorf("result = %v, want canned %v", out.ID, fake.canned.ID)
	}
}

// Remote flat query round-trips result and error over HTTP/JSON.
func TestSmokeFlatQueryRemote(t *testing.T) {
	t.Parallel()
	fake := newFake()
	srv := besteffort.NewServer()
	chatbiz.RegisterChatBestEffort(srv, fake)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := chatbiz.NewChatBizClientRemote("http://unused", ts.URL)

	out, err := c.GetConversation(context.Background(), fake.canned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != fake.canned.ID {
		t.Errorf("round-trip result = %v, want %v", out.ID, fake.canned.ID)
	}

	_, err = c.GetConversation(context.Background(), sentinelID)
	if err == nil {
		t.Fatal("expected error from sentinel")
	}
	_, code, _, ok := errors.Decompose(err)
	if !ok || code != "smoke_conflict" {
		t.Errorf("decomposed code = %q (ok=%v), want smoke_conflict", code, ok)
	}
	if !restate.IsTerminalError(err) {
		t.Errorf("round-tripped error is not terminal: %v", err)
	}
}
