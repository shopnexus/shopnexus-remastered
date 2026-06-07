package restatec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallDecodesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Svc/Method" {
			t.Errorf("path = %s, want /Svc/Method", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Value":42}`))
	}))
	defer srv.Close()

	out, err := Call[struct{ Value int }](context.Background(), NewCallClient(srv.URL), "Svc", "Method", map[string]int{"in": 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Value != 42 {
		t.Errorf("Value = %d, want 42", out.Value)
	}
}

func TestCallNon200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom","code":500}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Call[int](context.Background(), NewCallClient(srv.URL), "Svc", "Method", nil)
	if err == nil {
		t.Fatal("want error on 500")
	}
}

func TestCallVoidAwaitsAndDiscardsBody(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	if err := CallVoid(context.Background(), NewCallClient(srv.URL), "Svc", "Method", nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("server not called")
	}
}

func TestCallVoidNon200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom","code":500}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := CallVoid(context.Background(), NewCallClient(srv.URL), "Svc", "Method", nil); err == nil {
		t.Fatal("want error on 500")
	}
}

func TestSendUsesSendSuffixAndAccepts202(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Svc/Method/send" {
			t.Errorf("path = %s, want /Svc/Method/send", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"invocationId":"inv_1","status":"Accepted"}`))
	}))
	defer srv.Close()

	if err := Send(context.Background(), NewSendClient(srv.URL), "Svc", "Method", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSendNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom","code":400}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	if err := Send(context.Background(), NewSendClient(srv.URL), "Svc", "Method", nil); err == nil {
		t.Fatal("want error on 400")
	}
}
