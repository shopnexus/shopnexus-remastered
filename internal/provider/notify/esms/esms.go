// Package esms sends SMS through eSMS.vn, the Vietnamese aggregator this deployment
// buys its brandname from.
//
// Two things about the local market shape this client. The message content has to
// match a template registered with the carriers, so the text is configuration rather
// than a string in this file; and the API answers 200 with a status *code in the body*,
// so a delivery failure has to be read out of the payload rather than the HTTP status.
package esms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"shopnexus/internal/provider/notify"
)

// Name is the SMS_PROVIDER value that selects this vendor.
const Name = "esms"

const (
	sendPath = "/MainService.svc/json/SendMultipleMessage_V4_post_json"
	// codeSuccess is the only CodeResult that means the message was accepted.
	codeSuccess = "100"
	// maxErrorBody caps what is read from a failure response, so a provider returning
	// an HTML error page cannot pull megabytes into a log line.
	maxErrorBody = 4 << 10
)

type Config struct {
	// BaseURL is the API root, normally "https://rest.esms.vn".
	BaseURL   string
	APIKey    string
	SecretKey string
	// Brandname is the registered sender. An unregistered one is rejected by the
	// aggregator, not by us.
	Brandname string
	// SMSType is eSMS's message class, which follows from the contract — customer-care
	// versus advertising traffic are priced and routed differently, so it is not ours
	// to guess.
	SMSType string
	// ContentTemplate renders the message body and must match the template registered
	// with the carriers. It is given {{.Code}}.
	ContentTemplate string
	// Unicode says whether the content carries diacritics. It doubles the cost per
	// segment, so a template written without them should leave it false.
	Unicode bool
	// Sandbox asks eSMS to accept and drop the message, for a staging deployment that
	// should not spend credit or ring a real phone.
	Sandbox bool
	// Timeout bounds one send. Required: an OTP the user is waiting for has to fail
	// fast enough for them to press the button again.
	Timeout time.Duration
	// HTTPClient is optional and must not carry a Timeout of its own — the budget above
	// is applied to the request context, so an instrumented transport can be layered in.
	HTTPClient *http.Client
}

var _ notify.SMSSender = (*Client)(nil)

type Client struct {
	cfg     Config
	http    *http.Client
	content *template.Template
}

func NewClient(cfg Config) (*Client, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, errors.New("esms config: base url is required")
	case cfg.APIKey == "":
		return nil, errors.New("esms config: api key is required")
	case cfg.SecretKey == "":
		return nil, errors.New("esms config: secret key is required")
	case cfg.Brandname == "":
		return nil, errors.New("esms config: brandname is required")
	case cfg.SMSType == "":
		return nil, errors.New("esms config: sms type is required")
	case cfg.ContentTemplate == "":
		return nil, errors.New("esms config: content template is required")
	case cfg.Timeout <= 0:
		return nil, errors.New("esms config: timeout is required")
	}
	content, err := template.New("content").Parse(cfg.ContentTemplate)
	if err != nil {
		return nil, fmt.Errorf("esms config: parse content template: %w", err)
	}
	// A template that renders without the code is the worst kind of misconfiguration:
	// every send succeeds and every user is stuck. Catch it at startup.
	probe, err := render(content, "424242")
	if err != nil {
		return nil, fmt.Errorf("esms config: render content template: %w", err)
	}
	if !strings.Contains(probe, "424242") {
		return nil, errors.New("esms config: content template does not include {{.Code}}")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient, content: content}, nil
}

// sendRequest is eSMS's payload. The credentials travel in the body, which is their
// design, not a choice available here.
type sendRequest struct {
	APIKey    string `json:"ApiKey"`
	SecretKey string `json:"SecretKey"`
	Phone     string `json:"Phone"`
	Content   string `json:"Content"`
	Brandname string `json:"Brandname"`
	SMSType   string `json:"SmsType"`
	IsUnicode string `json:"IsUnicode"`
	Sandbox   string `json:"Sandbox"`
}

type sendResponse struct {
	CodeResult   string `json:"CodeResult"`
	SMSID        string `json:"SMSID"`
	ErrorMessage string `json:"ErrorMessage"`
}

func (c *Client) SendSMS(ctx context.Context, m notify.Message) error {
	if m.Phone == "" {
		return notify.ErrNoChannel
	}
	if m.Kind != notify.KindPhoneCode {
		// Only the code goes by SMS. A reset link in a 160-character message is a bad
		// idea, and sending one silently would be worse than refusing.
		return fmt.Errorf("esms: kind %q is not sent over sms", m.Kind)
	}
	content, err := render(c.content, m.Token)
	if err != nil {
		return fmt.Errorf("render sms content: %w", err)
	}

	body, err := json.Marshal(sendRequest{
		APIKey:    c.cfg.APIKey,
		SecretKey: c.cfg.SecretKey,
		Phone:     localPhone(m.Phone),
		Content:   content,
		Brandname: c.cfg.Brandname,
		SMSType:   c.cfg.SMSType,
		IsUnicode: boolFlag(c.cfg.Unicode),
		Sandbox:   boolFlag(c.cfg.Sandbox),
	})
	if err != nil {
		return fmt.Errorf("encode esms request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+sendPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build esms request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call esms: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("esms returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out sendResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode esms response: %w", err)
	}
	// The status lives in the body: a rejected message still comes back 200.
	if out.CodeResult != codeSuccess {
		if out.ErrorMessage != "" {
			return fmt.Errorf("esms rejected the message: %s (code %s)", out.ErrorMessage, out.CodeResult)
		}
		return fmt.Errorf("esms rejected the message: code %s", out.CodeResult)
	}
	return nil
}

func render(t *template.Template, code string) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ Code string }{Code: code}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// localPhone converts the E.164 number the account module stores into the digits eSMS
// expects: the country code without the leading plus, e.g. "+84901234567" ->
// "84901234567". The aggregator accepts that form for Vietnamese and foreign numbers
// alike, so there is nothing per-country to special-case here.
func localPhone(e164 string) string {
	return strings.TrimPrefix(strings.TrimSpace(e164), "+")
}

// boolFlag is eSMS's "0"/"1" spelling of a boolean.
func boolFlag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
