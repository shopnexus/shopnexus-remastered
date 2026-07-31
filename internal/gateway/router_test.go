package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	openapi "shopnexus/api"
	"shopnexus/internal/gateway"
	"shopnexus/internal/gateway/handler"
	"shopnexus/internal/infra/cache"
	accountapi "shopnexus/internal/module/account/api"
	"shopnexus/internal/module/account/api/accounttest"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	commonapi "shopnexus/internal/module/common/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/session"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

// stubAccount answers the routes these tests exercise and inherits a 501 for the rest, so a
// documented route nobody wired up is still distinguishable from one that works.
type stubAccount struct{ accounttest.Stub }

func (stubAccount) GetMe(context.Context, accountapi.GetMeRequest) (accountapi.Me, error) {
	return accountapi.Me{ID: id.Of[id.Account](1)}, nil
}

type stubCat struct{}

var _ catalogapi.Service = stubCat{}

func (stubCat) ListCategories(context.Context, catalogapi.ListCategoriesRequest) ([]catalogapi.Category, error) {
	return nil, nil
}
func (stubCat) AdminCreateCategory(context.Context, catalogapi.CreateCategoryRequest) (catalogapi.Category, error) {
	return catalogapi.Category{ID: id.Of[id.Category](1)}, nil
}
func (stubCat) AdminUpdateCategory(context.Context, catalogapi.UpdateCategoryRequest) (catalogapi.Category, error) {
	return catalogapi.Category{ID: id.Of[id.Category](1)}, nil
}
func (stubCat) AdminDeleteCategory(context.Context, catalogapi.DeleteCategoryRequest) error {
	return nil
}

type stubOrder struct{}

func (stubOrder) PlaceOrder(context.Context, orderapi.PlaceOrderRequest) (orderapi.Order, error) {
	return orderapi.Order{ID: id.Of[id.Order](1), Status: "pending"}, nil
}
func (stubOrder) GetOrder(context.Context, orderapi.GetOrderRequest) (orderapi.Order, error) {
	return orderapi.Order{ID: id.Of[id.Order](1)}, nil
}

type stubChat struct{}

func (stubChat) SendMessage(context.Context, chatapi.SendMessageRequest) (chatapi.Message, error) {
	return chatapi.Message{ID: id.Of[id.Message](1)}, nil
}
func (stubChat) ListMessages(context.Context, chatapi.ListMessagesRequest) ([]chatapi.Message, error) {
	return nil, nil
}

type stubCommon struct{}

func (stubCommon) RegisterResource(context.Context, commonapi.RegisterResourceRequest) (commonapi.Resource, error) {
	return commonapi.Resource{ID: id.Of[id.Resource](1)}, nil
}
func (stubCommon) GetResources(context.Context, commonapi.GetResourcesRequest) ([]commonapi.Resource, error) {
	return nil, nil
}
func (stubCommon) ListOptions(context.Context, commonapi.ListOptionsRequest) ([]commonapi.Option, error) {
	return nil, nil
}

type stubPayment struct{}

func (stubPayment) CreateSession(context.Context, financeapi.CreateSessionRequest) (financeapi.Session, error) {
	return financeapi.Session{ID: id.Of[id.PaymentSession](1)}, nil
}
func (stubPayment) GetSession(context.Context, financeapi.GetSessionRequest) (financeapi.Session, error) {
	return financeapi.Session{ID: id.Of[id.PaymentSession](1)}, nil
}
func (stubPayment) GetWallet(context.Context, financeapi.GetWalletRequest) (financeapi.Wallet, error) {
	return financeapi.Wallet{Currency: "VND"}, nil
}

type stubTrust struct{}

func (stubTrust) SubmitFeedback(context.Context, trustapi.SubmitFeedbackRequest) (trustapi.Feedback, error) {
	return trustapi.Feedback{ID: id.Of[id.Feedback](1)}, nil
}
func (stubTrust) GetReputation(context.Context, trustapi.GetReputationRequest) (trustapi.Reputation, error) {
	return trustapi.Reputation{Role: "seller"}, nil
}
func (stubTrust) SubmitReport(context.Context, trustapi.SubmitReportRequest) (trustapi.Report, error) {
	return trustapi.Report{ID: id.Of[id.Report](1)}, nil
}

func newRouter() (http.Handler, *token.Manager, *session.Store) {
	v := validation.Default()
	log := slog.Default()
	tm := token.NewManager("0123456789012345678901234567890123", time.Hour)
	sessions := session.New(cache.NewInMemoryClient(), time.Hour)
	return gateway.NewRouter(gateway.Deps{
		Account:  handler.NewAccount(stubAccount{}, v, log),
		Catalog:  handler.NewCatalog(stubCat{}, v, log),
		Order:    handler.NewOrder(stubOrder{}, v, log),
		Chat:     handler.NewChat(stubChat{}, v, log),
		Common:   handler.NewCommon(stubCommon{}, v, log),
		Finance:  handler.NewFinance(stubPayment{}, v, log),
		Trust:    handler.NewTrust(stubTrust{}, v, log),
		Tokens:   tm,
		Sessions: sessions,
		Log:      log,
	}), tm, sessions
}

// bearer opens a real session and mints the token that names it: the middleware checks both,
// so a hand-built token is not enough to reach a handler.
func bearer(t *testing.T, tm *token.Manager, sessions *session.Store, accountID int64) string {
	t.Helper()
	sess, err := sessions.Create(context.Background(), accountID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	tok, err := tm.Issue(token.Claims{AccountID: id.Of[id.Account](accountID).String(), SessionID: sess.ID})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// The scaffold distinguishes three answers, and each is a different fact about the
// request: 404 means no such route, 401 means the route needs a token, and 501
// means the route is wired and documented but not written yet. Getting them
// confused is what makes a client debug the wrong end of the problem.

func TestRouter_UndocumentedPathIs404(t *testing.T) {
	r, _, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, openapi.BasePath+"/no-such-route", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// A public route reaches its handler with no Authorization header.
func TestRouter_PublicRouteNeedsNoToken(t *testing.T) {
	r, _, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, openapi.BasePath+"/listings/"+id.Of[id.Listing](1).String(), nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestRouter_AuthenticatedRouteRejectsNoToken(t *testing.T) {
	r, _, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, openapi.BasePath+"/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// With a live session the middleware hands off, so the handler is what answers.
func TestRouter_AuthenticatedRouteWithToken(t *testing.T) {
	r, tm, sessions := newRouter()
	req := httptest.NewRequest(http.MethodGet, openapi.BasePath+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+bearer(t, tm, sessions, 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRouter_ConfirmOrderRequiresAuth(t *testing.T) {
	r, _, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, openapi.BasePath+"/orders", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// The admin surface is behind the same middleware; the role check itself is the
// handler's, so an anonymous caller stops at 401 rather than 403.
func TestRouter_AdminRouteRequiresAuth(t *testing.T) {
	r, _, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, openapi.BasePath+"/admin/reports", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// The request id has to survive the whole way out: the logging middleware mints it, the
// header carries it, and the error body repeats it. A user reporting "it failed" then
// hands over a value that is on the log line for their exact request — which is the only
// reason to put it in the body at all.
func TestRouter_RequestIDReachesHeaderAndErrorBody(t *testing.T) {
	r, _, _ := newRouter()

	// An authenticated route without a token answers 401 through httpx.WriteError, so this
	// exercises the real error path. Deliberately not a 501 route: those disappear as the
	// modules get implemented, and this test is about the middleware, not about which
	// module happens to be a stub today. A 404 would not do either — that one comes from
	// ServeMux and never reaches the error writer.
	req := httptest.NewRequest(http.MethodGet, openapi.BasePath+"/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	hdr := rec.Header().Get("X-Request-Id")
	if hdr == "" {
		t.Fatal("X-Request-Id response header is empty")
	}

	var body struct {
		Data  any `json:"data"`
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v — body %s", err, rec.Body.String())
	}
	if body.Data != nil {
		t.Error("an error response must not carry data")
	}
	if body.Error.Code != "unauthorized" {
		t.Errorf("code = %q", body.Error.Code)
	}
	if body.Error.RequestID != hdr {
		t.Errorf("body request_id = %q but header = %q; they must be the same value", body.Error.RequestID, hdr)
	}
}
