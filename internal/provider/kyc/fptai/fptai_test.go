package fptai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopnexus/internal/provider/kyc"
	"shopnexus/internal/provider/kyc/fptai"
)

// vendor stands in for api.fpt.ai and for the storage the scans are fetched from, so a
// test exercises the whole path: download, recognise, face match.
type vendor struct {
	idr       string
	checkface string

	sawAPIKey  string
	idrCalls   int
	faceCalls  int
	scanServed int
}

func (v *vendor) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/vision/idr/vnm", func(w http.ResponseWriter, r *http.Request) {
		v.idrCalls++
		v.sawAPIKey = r.Header.Get("api-key")
		writeJSON(w, v.idr)
	})
	mux.HandleFunc("/dmp/checkface/v1", func(w http.ResponseWriter, r *http.Request) {
		v.faceCalls++
		writeJSON(w, v.checkface)
	})
	// The scans themselves: whatever the storage layer's short-lived URL points at.
	mux.HandleFunc("/scans/", func(w http.ResponseWriter, _ *http.Request) {
		v.scanServed++
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("not-really-a-jpeg"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func newClient(t *testing.T, baseURL string) kyc.Client {
	t.Helper()
	c, err := fptai.NewClient(fptai.Config{
		BaseURL:         baseURL,
		APIKey:          "fpt-key",
		RequestTimeout:  2 * time.Second,
		DownloadTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func params(baseURL, docType string) kyc.CheckParams {
	return kyc.CheckParams{
		AccountRef: "acc_62mxefynht57b",
		DocType:    docType,
		Locale:     "vi-VN",
		Front:      kyc.Image{URL: baseURL + "/scans/front", Mime: "image/jpeg"},
		Back:       kyc.Image{URL: baseURL + "/scans/back", Mime: "image/jpeg"},
		Selfie:     kyc.Image{URL: baseURL + "/scans/selfie", Mime: "image/jpeg"},
	}
}

// The happy path: the card reads, the face matches, and the expiry on the document comes
// back — the payout gate needs that date as much as it needs the status.
func TestCheck_VerifiedCarriesTheDocumentExpiry(t *testing.T) {
	v := &vendor{
		idr:       `{"errorCode":0,"errorMessage":"","data":[{"doe":"01/02/2030","type":"cccd_12_front"}]}`,
		checkface: `{"code":"200","data":{"isMatch":true,"similarity":96.4}}`,
	}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusVerified {
		t.Fatalf("status = %q, want verified", got.Status)
	}
	if got.Provider != "fpt-ai" || got.Ref == "" {
		t.Errorf("result = %+v, want the vendor named and a case ref", got)
	}
	if got.ExpiresAt == nil || got.ExpiresAt.Format("2006-01-02") != "2030-02-01" {
		t.Errorf("expires_at = %v, want 2030-02-01", got.ExpiresAt)
	}
	if v.sawAPIKey != "fpt-key" {
		t.Errorf("api-key header = %q", v.sawAPIKey)
	}
}

// A card that says it never expires has no expiry — which is different from one whose date
// could not be read, and only the second is a reason to send the photo back.
func TestCheck_NoExpiryDocumentIsStillVerified(t *testing.T) {
	v := &vendor{
		idr:       `{"errorCode":0,"data":[{"doe":"Không thời hạn"}]}`,
		checkface: `{"code":"200","data":{"isMatch":true,"similarity":91}}`,
	}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusVerified || got.ExpiresAt != nil {
		t.Fatalf("result = %+v, want verified with no expiry", got)
	}
}

// A scan the vendor cannot use is the account's problem to fix, so it comes back as a
// rejection carrying the vendor's own words rather than as a failed request.
func TestCheck_UnreadableScanIsRejectedWithTheVendorsReason(t *testing.T) {
	v := &vendor{idr: `{"errorCode":9,"errorMessage":"No id card found"}`}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusRejected {
		t.Fatalf("status = %q, want rejected", got.Status)
	}
	if got.RejectionReason != "No id card found" {
		t.Errorf("reason = %q", got.RejectionReason)
	}
	// No point comparing a face against a card that was never read.
	if v.faceCalls != 0 {
		t.Errorf("face match was called %d times after an unreadable card", v.faceCalls)
	}
}

func TestCheck_FaceMismatchIsRejected(t *testing.T) {
	v := &vendor{
		idr:       `{"errorCode":0,"data":[{"doe":"01/02/2030"}]}`,
		checkface: `{"code":"200","data":{"isMatch":false,"similarity":12.3}}`,
	}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusRejected || got.RejectionReason == "" {
		t.Fatalf("result = %+v, want a rejection with a reason", got)
	}
}

// A borderline "match" is refused too: this gates payouts, and the vendor's boolean is
// more generous than that decision deserves.
func TestCheck_LowSimilarityIsRejected(t *testing.T) {
	v := &vendor{
		idr:       `{"errorCode":0,"data":[{"doe":"01/02/2030"}]}`,
		checkface: `{"code":"200","data":{"isMatch":true,"similarity":55}}`,
	}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusRejected {
		t.Fatalf("status = %q, want rejected on a weak match", got.Status)
	}
}

// This client reads Vietnamese ID cards. A passport is left pending for a moderator: "we
// did not check this" and "this is not you" are different answers.
func TestCheck_UnsupportedDocTypeIsPendingWithNoVendorCall(t *testing.T) {
	v := &vendor{}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "passport"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusPending {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if v.idrCalls != 0 || v.scanServed != 0 {
		t.Errorf("the vendor was called for a document type it does not read: idr=%d scans=%d", v.idrCalls, v.scanServed)
	}
}

// An expiry that cannot be parsed sends the scan back rather than passing a card whose
// validity nobody knows.
func TestCheck_UnreadableExpiryIsRejected(t *testing.T) {
	v := &vendor{idr: `{"errorCode":0,"data":[{"doe":"??"}]}`}
	srv := v.start(t)

	got, err := newClient(t, srv.URL).Check(context.Background(), params(srv.URL, "national-id"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != kyc.StatusRejected || !strings.Contains(got.RejectionReason, "expiry") {
		t.Fatalf("result = %+v, want a rejection about the expiry", got)
	}
}

// A storage URL that 404s is an infrastructure failure, not a verdict about the person.
func TestCheck_MissingScanIsAnError(t *testing.T) {
	v := &vendor{idr: `{"errorCode":0,"data":[{"doe":"01/02/2030"}]}`}
	srv := v.start(t)
	p := params(srv.URL, "national-id")
	p.Front.URL = srv.URL + "/nope"

	if _, err := newClient(t, srv.URL).Check(context.Background(), p); err == nil {
		t.Fatal("expected an error when a scan cannot be downloaded")
	}
}

func TestNewClient_RequiredFields(t *testing.T) {
	for name, cfg := range map[string]fptai.Config{
		"base url":         {APIKey: "k", RequestTimeout: time.Second, DownloadTimeout: time.Second},
		"api key":          {BaseURL: "https://api.fpt.ai", RequestTimeout: time.Second, DownloadTimeout: time.Second},
		"request timeout":  {BaseURL: "https://api.fpt.ai", APIKey: "k", DownloadTimeout: time.Second},
		"download timeout": {BaseURL: "https://api.fpt.ai", APIKey: "k", RequestTimeout: time.Second},
	} {
		if _, err := fptai.NewClient(cfg); err == nil {
			t.Errorf("expected an error when %s is missing", name)
		}
	}
}
