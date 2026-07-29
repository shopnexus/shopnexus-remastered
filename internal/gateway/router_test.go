package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	"shopnexus/internal/gateway"
	"shopnexus/internal/gateway/handler"
	accountapi "shopnexus/internal/module/account/api"
	catalogapi "shopnexus/internal/module/catalog/api"
	chatapi "shopnexus/internal/module/chat/api"
	commonapi "shopnexus/internal/module/common/api"
	financeapi "shopnexus/internal/module/finance/api"
	orderapi "shopnexus/internal/module/order/api"
	trustapi "shopnexus/internal/module/trust/api"
	"shopnexus/internal/shared/id"
	"shopnexus/internal/shared/id/idtest"
	"shopnexus/internal/shared/token"
	"shopnexus/internal/shared/validation"
)

func TestMain(m *testing.M) { idtest.Install(); m.Run() }

type stubAccount struct{}

func (stubAccount) Register(context.Context, accountapi.RegisterRequest) (accountapi.Profile, error) {
	return accountapi.Profile{ID: id.Of[id.Account](1)}, nil
}
func (stubAccount) Login(context.Context, accountapi.LoginRequest) (accountapi.Token, error) {
	return accountapi.Token{AccessToken: "t"}, nil
}
func (stubAccount) GetProfile(context.Context, accountapi.GetProfileRequest) (accountapi.Profile, error) {
	return accountapi.Profile{ID: id.Of[id.Account](1)}, nil
}

type stubCat struct{}

func (stubCat) CreateListing(context.Context, catalogapi.CreateListingRequest) (catalogapi.Listing, error) {
	return catalogapi.Listing{}, nil
}
func (stubCat) GetListing(context.Context, catalogapi.GetListingRequest) (catalogapi.Listing, error) {
	return catalogapi.Listing{ID: id.Of[id.ProductSPU](1)}, nil
}
func (stubCat) ListListings(context.Context, catalogapi.ListListingsRequest) ([]catalogapi.Listing, error) {
	return nil, nil
}
func (stubCat) SetStock(context.Context, catalogapi.SetStockRequest) (catalogapi.Stock, error) {
	return catalogapi.Stock{}, nil
}
func (stubCat) GetStock(context.Context, catalogapi.GetStockRequest) (catalogapi.Stock, error) {
	return catalogapi.Stock{}, nil
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

func newRouter() (http.Handler, *token.Manager) {
	v := validation.Default()
	log := slog.Default()
	tm := token.NewManager("0123456789012345678901234567890123", time.Hour)
	return gateway.NewRouter(gateway.Deps{
		Account: handler.NewAccount(stubAccount{}, v, log),
		Catalog: handler.NewCatalog(stubCat{}, v, log),
		Order:   handler.NewOrder(stubOrder{}, v, log),
		Chat:    handler.NewChat(stubChat{}, v, log),
		Common:  handler.NewCommon(stubCommon{}, v, log),
		Finance: handler.NewFinance(stubPayment{}, v, log),
		Trust:   handler.NewTrust(stubTrust{}, v, log),
		Tokens:  tm,
		Log:     log,
	}), tm
}

// The scaffold distinguishes three answers, and each is a different fact about the
// request: 404 means no such route, 401 means the route needs a token, and 501
// means the route is wired and documented but not written yet. Getting them
// confused is what makes a client debug the wrong end of the problem.

func TestRouter_UndocumentedPathIs404(t *testing.T) {
	r, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-route", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// A public route reaches its handler with no Authorization header.
func TestRouter_PublicRouteNeedsNoToken(t *testing.T) {
	r, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/listings/"+id.Of[id.ProductSPU](1).String(), nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestRouter_AuthenticatedRouteRejectsNoToken(t *testing.T) {
	r, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// With a token the middleware hands off, so the handler is what answers.
func TestRouter_AuthenticatedRouteWithToken(t *testing.T) {
	r, tm := newRouter()
	tok, _ := tm.Issue(id.Of[id.Account](1).String())
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestRouter_ConfirmOrderRequiresAuth(t *testing.T) {
	r, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// The admin surface is behind the same middleware; the role check itself is the
// handler's, so an anonymous caller stops at 401 rather than 403.
func TestRouter_AdminRouteRequiresAuth(t *testing.T) {
	r, _ := newRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/reports", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
