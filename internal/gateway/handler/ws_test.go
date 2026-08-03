package handler_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"shopnexus/internal/gateway/handler"
	"shopnexus/internal/gateway/ws"
	"shopnexus/internal/infra/cache"
	"shopnexus/internal/shared/session"
)

// noopFanout satisfies realtime.Fanout without a bus: these tests are about the socket's
// lifetime, not about delivery.
type noopFanout struct{}

func (noopFanout) Broadcast(string, []byte) error { return nil }

func (noopFanout) OnBroadcast(string, func([]byte)) (func(), error) {
	return func() {}, nil
}

// pingInterval doubles as the session re-check interval, so it is short here to keep the
// test fast. Production reads it from WS_PING_INTERVAL.
const testPing = 100 * time.Millisecond

type wsFixture struct {
	server   *httptest.Server
	tickets  *session.Tickets
	sessions *session.Store
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()

	c := cache.NewInMemoryClient()
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close cache: %v", err)
		}
	})

	sessions := session.New(c, time.Hour)
	tickets := session.NewTickets(c, 30*time.Second)
	log := slog.New(slog.DiscardHandler)
	hub := ws.NewHub(noopFanout{}, log, ws.Config{SendBuffer: 8, MaxPerAccount: 4})

	h := handler.NewWS(hub, tickets, sessions, log, time.Second, testPing, []string{"*"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", h.Connect)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &wsFixture{server: srv, tickets: tickets, sessions: sessions}
}

// dial opens a socket with a freshly issued ticket for a new session, and returns the
// session id so a test can revoke it.
func (f *wsFixture) dial(t *testing.T, accountID int64) (*websocket.Conn, string) {
	t.Helper()

	sess, err := f.sessions.Create(t.Context(), accountID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ticket, err := f.tickets.Issue(t.Context(), accountID, sess.ID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws?ticket=" + ticket
	conn, _, err := websocket.Dial(t.Context(), url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, sess.ID
}

// A socket must not outlive the session that authorised it. Checking only at the
// handshake would make this the one authenticated surface a logout cannot reach.
func TestConnectClosesWhenTheSessionIsRevoked(t *testing.T) {
	f := newWSFixture(t)
	conn, sessionID := f.dial(t, 42)

	if err := f.sessions.Revoke(t.Context(), sessionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Read blocks until the server closes; the re-check runs on the ping tick.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("read succeeded; the socket outlived its revoked session")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want %v (err: %v)", got, websocket.StatusPolicyViolation, err)
	}
}

// Revoking every session of an account is an epoch bump rather than a key delete, so it
// has to be caught by the same check.
func TestConnectClosesWhenEverySessionIsRevoked(t *testing.T) {
	f := newWSFixture(t)
	conn, _ := f.dial(t, 42)

	if err := f.sessions.RevokeAll(t.Context(), 42, ""); err != nil {
		t.Fatalf("revoke all: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded; an epoch bump did not end the socket")
	}
}

// The converse: a live session must survive several re-checks, or the fix would just be a
// slow disconnect for everybody.
func TestConnectStaysOpenWhileTheSessionLives(t *testing.T) {
	f := newWSFixture(t)
	conn, _ := f.dial(t, 42)

	ctx, cancel := context.WithTimeout(t.Context(), 4*testPing)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != -1 {
		t.Fatalf("socket was closed while its session was still valid: %v", err)
	}
}

func TestConnectRefusesAnAlreadyRedeemedTicket(t *testing.T) {
	f := newWSFixture(t)

	sess, err := f.sessions.Create(t.Context(), 42)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ticket, err := f.tickets.Issue(t.Context(), 42, sess.ID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws?ticket=" + ticket

	first, _, err := websocket.Dial(t.Context(), url, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer func() { _ = first.CloseNow() }()

	if _, _, err := websocket.Dial(t.Context(), url, nil); err == nil {
		t.Fatal("second dial with the same ticket succeeded; tickets must be single-use")
	}
}

// A ticket for a session that was revoked between issue and use must not open a socket.
func TestConnectRefusesATicketForADeadSession(t *testing.T) {
	f := newWSFixture(t)

	sess, err := f.sessions.Create(t.Context(), 42)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ticket, err := f.tickets.Issue(t.Context(), 42, sess.ID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	if err := f.sessions.Revoke(t.Context(), sess.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws?ticket=" + ticket
	if _, _, err := websocket.Dial(t.Context(), url, nil); err == nil {
		t.Fatal("dial succeeded with a ticket whose session was already revoked")
	}
}

func TestConnectRefusesAMissingOrUnknownTicket(t *testing.T) {
	f := newWSFixture(t)
	base := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws"

	for _, tt := range []struct {
		name string
		url  string
	}{
		{"no ticket", base},
		{"unknown ticket", base + "?ticket=wst_deadbeef"},
		{"empty ticket", base + "?ticket="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := websocket.Dial(t.Context(), tt.url, nil); err == nil {
				t.Fatal("dial succeeded without a valid ticket")
			}
		})
	}
}
