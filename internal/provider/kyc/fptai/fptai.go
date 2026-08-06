// Package fptai implements the kyc.Client against FPT.AI eKYC.
//
// FPT.AI is a read-the-scan vendor, not a hosted-flow one: the check is two calls that
// answer immediately, so a case comes back already decided and there is no session URL
// to send the user to.
//
//   - /vision/idr/vnm reads a Vietnamese ID card and returns its fields, including the
//     expiry date the payout gate needs.
//   - /dmp/checkface/v1 compares the portrait on that card with the selfie.
//
// Both answer HTTP 200 with a status *inside* the body, so a failure has to be read out
// of the payload rather than off the status line.
//
// Document types FPT.AI does not read here — a passport, a driver licence — come back
// pending rather than rejected: "we did not check this" and "this is not you" are
// different answers, and only a moderator can give the first one a verdict.
package fptai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"shopnexus/internal/provider/kyc"
	"shopnexus/internal/shared/httpx"
)

const (
	// Name is the KYC_PROVIDER value that selects this vendor, and the value written to
	// identity_document.provider. One declaration, so config, wiring and the row agree.
	Name            = "fpt-ai"
	idRecognizePath = "/vision/idr/vnm"
	faceMatchPath   = "/dmp/checkface/v1"

	// maxScanBytes caps a downloaded scan. A phone camera photo is a few megabytes; a
	// URL that streams forever must not be able to fill this process's memory.
	maxScanBytes = 12 << 20
	// maxErrorBody caps what is read from an unexpected response body for the error message.
	maxErrorBody = 4 << 10

	// faceMatchThreshold is the similarity below which the selfie is treated as somebody
	// else. FPT.AI returns both a boolean and a score; the score is used as well because
	// a borderline "match" on a payout gate is worth refusing.
	faceMatchThreshold = 80.0
)

// docExpiryLayout is how FPT.AI prints dates, and noExpiry is what it prints for a
// document that does not run out.
const docExpiryLayout = "02/01/2006"

var noExpiry = []string{"không thời hạn", "khong thoi han", "no expiry"}

type Config struct {
	// BaseURL is the API root, normally "https://api.fpt.ai".
	BaseURL string
	// APIKey is sent as the api-key header.
	APIKey string
	// RequestTimeout bounds one vendor call. Required: the call is on a request path and
	// image recognition is slow enough to need a stated budget.
	RequestTimeout time.Duration
	// DownloadTimeout bounds fetching one scan from storage. Required, and separate,
	// because it is a different dependency with a different failure mode.
	DownloadTimeout time.Duration
	// HTTPClient is optional and must not carry a Timeout of its own — the budgets above
	// are applied to the request contexts, so an instrumented transport can be layered in.
	HTTPClient *http.Client
}

var _ kyc.Client = (*Client)(nil)

type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, errors.New("fptai config: base url is required")
	case cfg.APIKey == "":
		return nil, errors.New("fptai config: api key is required")
	case cfg.RequestTimeout <= 0:
		return nil, errors.New("fptai config: request timeout is required")
	case cfg.DownloadTimeout <= 0:
		return nil, errors.New("fptai config: download timeout is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient}, nil
}

// nationalID is the only type this vendor reads through the endpoint wired here.
const nationalID = "national-id"

func (c *Client) Check(ctx context.Context, p kyc.CheckParams) (kyc.Result, error) {
	ref, err := caseRef()
	if err != nil {
		return kyc.Result{}, err
	}
	result := kyc.Result{Provider: Name, Ref: ref}

	if p.DocType != nationalID {
		// Left for a moderator, with no vendor call: guessing at a passport with a card
		// reader would produce a confident wrong answer.
		result.Status = kyc.StatusPending
		return result, nil
	}
	if !p.Front.Present() {
		return kyc.Result{}, errors.New("fptai: the front of the document is required")
	}

	front, err := c.download(ctx, p.Front)
	if err != nil {
		return kyc.Result{}, err
	}
	card, err := c.recognize(ctx, front)
	if err != nil {
		return kyc.Result{}, err
	}
	// A scan the vendor cannot read is the account's problem to fix, so it comes back as
	// a rejection with the vendor's own words rather than as a failed request.
	if card.reason != "" {
		result.Status = kyc.StatusRejected
		result.RejectionReason = card.reason
		return result, nil
	}

	if p.Selfie.Present() {
		selfie, err := c.download(ctx, p.Selfie)
		if err != nil {
			return kyc.Result{}, err
		}
		matched, reason, err := c.matchFace(ctx, front, selfie)
		if err != nil {
			return kyc.Result{}, err
		}
		if !matched {
			result.Status = kyc.StatusRejected
			result.RejectionReason = reason
			return result, nil
		}
	}

	result.Status = kyc.StatusVerified
	result.ExpiresAt = card.expiresAt
	return result, nil
}

// scan is a downloaded image, ready to be forwarded as a multipart part.
type scan struct {
	name string
	mime string
	data []byte
}

// download fetches one scan from storage. The URL is short-lived and belongs to the
// storage provider, so this client never constructs one.
func (c *Client) download(ctx context.Context, img kyc.Image) (scan, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.DownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.URL, nil)
	if err != nil {
		return scan{}, fmt.Errorf("build scan download request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return scan{}, fmt.Errorf("download scan: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return scan{}, fmt.Errorf("download scan: storage returned %d", resp.StatusCode)
	}

	// LimitReader plus one extra byte, so an object over the cap is reported rather than
	// silently truncated into an unreadable image.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxScanBytes+1))
	if err != nil {
		return scan{}, fmt.Errorf("read scan: %w", err)
	}
	if len(data) > maxScanBytes {
		return scan{}, fmt.Errorf("scan is larger than %d bytes", maxScanBytes)
	}
	mimeType := img.Mime
	if mimeType == "" {
		mimeType = resp.Header.Get("Content-Type")
	}
	return scan{name: "scan" + extensionFor(mimeType), mime: mimeType, data: data}, nil
}

// card is what this client keeps from the vendor's reading of a document: the expiry, and
// a reason when the scan was unusable. Deliberately not the id number, the name or the
// address — none of that is stored, so none of it is carried out of this package.
type card struct {
	expiresAt *time.Time
	reason    string
}

type idrResponse struct {
	ErrorCode    int    `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	Data         []struct {
		DOE  string `json:"doe"`
		Type string `json:"type"`
	} `json:"data"`
}

func (c *Client) recognize(ctx context.Context, front scan) (card, error) {
	body, contentType, err := multipartBody(map[string][]scan{"image": {front}})
	if err != nil {
		return card{}, err
	}
	var out idrResponse
	if err := c.post(ctx, idRecognizePath, contentType, body, &out); err != nil {
		return card{}, err
	}
	if out.ErrorCode != 0 {
		return card{reason: vendorReason(out.ErrorMessage, out.ErrorCode)}, nil
	}
	if len(out.Data) == 0 {
		return card{reason: "no document was found in the image"}, nil
	}
	expiry, err := parseExpiry(out.Data[0].DOE)
	if err != nil {
		// A card whose expiry could not be read is not a verified card: the gate reads
		// that date, so an unknown one has to send the scan back.
		return card{reason: "the expiry date on the document could not be read"}, nil
	}
	return card{expiresAt: expiry}, nil
}

type checkFaceResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		IsMatch    bool    `json:"isMatch"`
		Similarity float64 `json:"similarity"`
	} `json:"data"`
}

func (c *Client) matchFace(ctx context.Context, front, selfie scan) (matched bool, reason string, err error) {
	// Both images go in the same repeated field, which is how this endpoint takes its pair.
	body, contentType, err := multipartBody(map[string][]scan{"file[]": {front, selfie}})
	if err != nil {
		return false, "", err
	}
	var out checkFaceResponse
	if err := c.post(ctx, faceMatchPath, contentType, body, &out); err != nil {
		return false, "", err
	}
	if out.Code != "200" {
		return false, vendorReason(out.Message, 0), nil
	}
	if !out.Data.IsMatch || out.Data.Similarity < faceMatchThreshold {
		return false, "the selfie does not match the photo on the document", nil
	}
	return true, "", nil
}

// post sends one multipart request and decodes the JSON answer.
func (c *Client) post(ctx context.Context, path, contentType string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build fpt.ai request: %w", err)
	}
	req.Header.Set("api-key", c.cfg.APIKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call fpt.ai %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("fpt.ai %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := httpx.DecodeVendorJSON(resp.Body, out); err != nil {
		return fmt.Errorf("decode fpt.ai response: %w", err)
	}
	return nil
}

// multipartBody builds the upload. Buffered rather than streamed because the parts are
// already in memory and the vendor needs a Content-Length.
func multipartBody(parts map[string][]scan) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for field, images := range parts {
		for _, img := range images {
			part, err := w.CreateFormFile(field, img.name)
			if err != nil {
				return nil, "", fmt.Errorf("create multipart field %s: %w", field, err)
			}
			if _, err := part.Write(img.data); err != nil {
				return nil, "", fmt.Errorf("write multipart field %s: %w", field, err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart body: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// parseExpiry reads the date off the document. A card that says it never expires has no
// expiry rather than an unreadable one, which is a different outcome for the caller.
func parseExpiry(doe string) (*time.Time, error) {
	v := strings.ToLower(strings.TrimSpace(doe))
	if v == "" {
		return nil, errors.New("no expiry date on the document")
	}
	for _, forever := range noExpiry {
		if strings.Contains(v, forever) {
			return nil, nil
		}
	}
	t, err := time.Parse(docExpiryLayout, strings.TrimSpace(doe))
	if err != nil {
		return nil, fmt.Errorf("parse document expiry %q: %w", doe, err)
	}
	return &t, nil
}

// vendorReason keeps the vendor's own wording where there is one, because it is more
// specific than anything this package could invent ("glare on the card", "wrong side").
func vendorReason(message string, code int) string {
	if m := strings.TrimSpace(message); m != "" {
		return m
	}
	if code != 0 {
		return fmt.Sprintf("the vendor could not use this scan (code %d)", code)
	}
	return "the vendor could not use this scan"
}

// caseRef is this deployment's handle on the check. FPT.AI has no case id of its own —
// it answers and forgets — and the obvious substitute, the document number, is exactly
// what this schema refuses to store.
func caseRef() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// extensionFor names the upload part. The vendor sniffs the bytes, but a part with no
// extension is rejected by some of its gateways.
func extensionFor(mimeType string) string {
	switch {
	case strings.Contains(mimeType, "png"):
		return ".png"
	case strings.Contains(mimeType, "webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}
