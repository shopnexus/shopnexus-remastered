package esms_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/notify"
	"shopnexus/internal/provider/notify/esms"
)

func config(baseURL string) esms.Config {
	return esms.Config{
		BaseURL:         baseURL,
		APIKey:          "key",
		SecretKey:       "secret",
		Brandname:       "SHOPNEXUS",
		SMSType:         "2",
		ContentTemplate: "Ma xac thuc ShopNexus: {{.Code}}. Hieu luc 10 phut.",
		Timeout:         2 * time.Second,
	}
}

// serve stands in for rest.esms.vn and hands the decoded request body back to the test.
func serve(t *testing.T, status int, body string) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSendSMS_PostsTheRenderedCode(t *testing.T) {
	srv, got := serve(t, http.StatusOK, `{"CodeResult":"100","SMSID":"abc"}`)
	client, err := esms.NewClient(config(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = client.SendSMS(context.Background(), notify.Message{
		Kind: notify.KindPhoneCode, Phone: "+84901234567", Token: "123456",
	})
	if err != nil {
		t.Fatalf("SendSMS: %v", err)
	}

	content, _ := (*got)["Content"].(string)
	if !strings.Contains(content, "123456") {
		t.Errorf("Content = %q, want the code in it", content)
	}
	// The account module stores E.164; eSMS wants the digits without the plus.
	if phone, _ := (*got)["Phone"].(string); phone != "84901234567" {
		t.Errorf("Phone = %q, want 84901234567", phone)
	}
	if brand, _ := (*got)["Brandname"].(string); brand != "SHOPNEXUS" {
		t.Errorf("Brandname = %q", brand)
	}
}

// eSMS answers 200 with the outcome in the body, so a rejected message must not be read as
// a delivered one.
func TestSendSMS_BodyLevelFailureIsAnError(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, `{"CodeResult":"104","ErrorMessage":"Brandname is not registered"}`)
	client, err := esms.NewClient(config(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = client.SendSMS(context.Background(), notify.Message{
		Kind: notify.KindPhoneCode, Phone: "+84901234567", Token: "123456",
	})
	if err == nil {
		t.Fatal("a CodeResult other than 100 must be an error")
	}
	if !strings.Contains(err.Error(), "Brandname is not registered") {
		t.Errorf("error should carry the vendor's message, got %v", err)
	}
}

// A template that drops the code sends an SMS nobody can use, and every send would look
// successful. Catch it when the process starts instead.
func TestNewClient_TemplateWithoutTheCodeIsRefused(t *testing.T) {
	cfg := config("https://rest.esms.vn")
	cfg.ContentTemplate = "Ma xac thuc ShopNexus."
	if _, err := esms.NewClient(cfg); err == nil {
		t.Fatal("expected an error for a template that does not include {{.Code}}")
	}
}

func TestNewClient_RequiredFields(t *testing.T) {
	for name, mutate := range map[string]func(*esms.Config){
		"base url":  func(c *esms.Config) { c.BaseURL = "" },
		"api key":   func(c *esms.Config) { c.APIKey = "" },
		"brandname": func(c *esms.Config) { c.Brandname = "" },
		"sms type":  func(c *esms.Config) { c.SMSType = "" },
		"timeout":   func(c *esms.Config) { c.Timeout = 0 },
	} {
		cfg := config("https://rest.esms.vn")
		mutate(&cfg)
		if _, err := esms.NewClient(cfg); err == nil {
			t.Errorf("expected an error when %s is missing", name)
		}
	}
}

// Only the code goes by SMS: a reset link in a 160-character message would be truncated,
// and sending one silently is worse than refusing.
func TestSendSMS_RefusesALinkKind(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, `{"CodeResult":"100"}`)
	client, _ := esms.NewClient(config(srv.URL))

	err := client.SendSMS(context.Background(), notify.Message{
		Kind: notify.KindPasswordReset, Phone: "+84901234567", Token: "tok",
	})
	if err == nil {
		t.Fatal("expected an error for a kind that is not sent over sms")
	}
}
