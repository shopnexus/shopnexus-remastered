package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"shopnexus/internal/config"
	"shopnexus/internal/shared/validation"
)

// The tracked template is the fixture. A test that built its own document would drift from the file a
// person actually copies; this way one set of assertions covers both — the example is valid, and it
// stays valid when a field is added to the struct.
const templatePath = "config.example.yml"

// document reads the template as a tree a test can edit by path. Loading it back through the real
// `config.Load` is the point: the parse, the unknown-key check and every rule run.
func document(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read %s: %v", templatePath, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", templatePath, err)
	}
	return doc
}

// load writes the tree to a temp file, points CONFIG_FILE at it, and loads it.
func load(t *testing.T, doc map[string]any) (*config.Config, error) {
	t.Helper()
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}
	t.Setenv(config.FileVar, path)
	return config.Load(validation.Default())
}

// set, drop and blank edit one value by its dotted path — the same path a failure names, so a test
// reads like the fix it describes. `blank` is how an unused vendor block sits in a real document:
// present and empty, rather than absent.
func set(t *testing.T, doc map[string]any, path string, value any) {
	t.Helper()
	section, key := walk(t, doc, path)
	section[key] = value
}

func drop(t *testing.T, doc map[string]any, path string) {
	t.Helper()
	section, key := walk(t, doc, path)
	if _, ok := section[key]; !ok {
		t.Fatalf("%s is not in the template, so dropping it proves nothing", path)
	}
	delete(section, key)
}

func blank(t *testing.T, doc map[string]any, path string) {
	t.Helper()
	section, key := walk(t, doc, path)
	switch section[key].(type) {
	case int, float64:
		section[key] = 0
	default:
		section[key] = ""
	}
}

func walk(t *testing.T, doc map[string]any, path string) (map[string]any, string) {
	t.Helper()
	parts := strings.Split(path, ".")
	section := doc
	for _, part := range parts[:len(parts)-1] {
		next, ok := section[part].(map[string]any)
		if !ok {
			t.Fatalf("%s: %q is not a section of the template", path, part)
		}
		section = next
	}
	return section, parts[len(parts)-1]
}

// The template has to load, or the file somebody copies is one that does not work — and adding a
// required field without adding it here is exactly the drift this catches.
func TestLoad_TheTemplateIsValid(t *testing.T) {
	t.Setenv(config.FileVar, templatePath)

	cfg, err := config.Load(validation.Default())
	if err != nil {
		t.Fatalf("the committed template does not load: %v", err)
	}
	if cfg.GatewayAddr == "" || cfg.LogLevel == "" || len(cfg.PaymentProviders) == 0 {
		t.Fatalf("the template loaded but says nothing: %+v", cfg)
	}
	// A template shipping a working credential is one somebody deploys by accident. It is in git.
	if cfg.EmbeddingAPIKey != "" || cfg.SMTPPassword != "" || cfg.StripeSecretKey != "" {
		t.Error("the template carries a credential")
	}
}

// Every value is required. Dropping the environment is worth it precisely because a missing one is
// found at startup instead of by the first request that needed it.
func TestLoad_EveryValueIsRequired(t *testing.T) {
	paths := []string{
		"gateway.addr", "gateway.public_base_url", "gateway.allowed_origins", "gateway.log_level",
		"security.jwt_secret", "security.id_cipher_key", "security.instance_id",
		"database.account", "database.catalog", "database.order", "database.chat",
		"database.trust", "database.finance", "database.observability",
		"redis.addr", "redis.password", "nats.url",
		"storage.provider", "storage.root", "storage.secret", "storage.upload_ttl",
		"storage.download_ttl", "storage.max_upload_bytes", "storage.max_video_bytes",
		"storage.allowed_mimes",
		"embedding.provider", "embedding.dimensions", "embedding.interval",
		"embedding.batch_size", "embedding.max_text_chars",
		"llm.provider", "workflow.runtime", "workflow.sweep_interval",
		"email.provider", "sms.provider", "oauth.verifier", "kyc.provider",
		"payment.providers", "payment.return_url_hosts", "transport.providers",
		"websocket.ticket_ttl", "websocket.write_timeout", "websocket.ping_interval",
		"websocket.send_buffer", "websocket.max_per_account", "websocket.allowed_origins",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			doc := document(t)
			drop(t, doc, path)
			if _, err := load(t, doc); err == nil {
				t.Fatalf("%s is missing and the config loaded anyway", path)
			}
		})
	}
}

// A whole section left out is the same failure as one value left out: a nil section would otherwise
// validate every field inside it as a zero.
func TestLoad_AMissingSectionFails(t *testing.T) {
	for _, section := range []string{"gateway", "database", "storage", "payment", "websocket"} {
		t.Run(section, func(t *testing.T) {
			doc := document(t)
			delete(doc, section)
			if _, err := load(t, doc); err == nil {
				t.Fatalf("the %s section is missing and the config loaded anyway", section)
			}
		})
	}
}

// A key nobody reads is a setting somebody believes is in effect. With no defaults and no
// environment to fall back on, a typo is otherwise completely silent.
func TestLoad_AnUnknownKeyFails(t *testing.T) {
	doc := document(t)
	set(t, doc, "gateway.log_lvel", "info")

	_, err := load(t, doc)
	if err == nil {
		t.Fatal("an unknown key loaded")
	}
	if !strings.Contains(err.Error(), "log_lvel") {
		t.Errorf("error does not name the typo: %v", err)
	}
}

func TestLoad_MalformedValues(t *testing.T) {
	cases := []struct {
		path  string
		value any
		says  string
	}{
		// A duration is a string like "15m", and the message has to say so: this is the field a person
		// most often writes as a bare number.
		{"storage.upload_ttl", 900, "duration"},
		{"storage.upload_ttl", "15 minutes", "duration"},
		{"gateway.addr", "0.0.0.0", "Addr"},
		{"gateway.public_base_url", "not-a-url", "PublicBaseURL"},
		{"gateway.log_level", "verbose", "LogLevel"},
		// Below the minimum, which is what makes a token worth signing with.
		{"security.jwt_secret", "short", "JWTSecret"},
		{"storage.max_upload_bytes", 0, "MaxUploadBytes"},
		{"workflow.runtime", "temporal", "Runtime"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s=%v", tc.path, tc.value), func(t *testing.T) {
			doc := document(t)
			set(t, doc, tc.path, tc.value)

			_, err := load(t, doc)
			if err == nil {
				t.Fatalf("%s = %v loaded", tc.path, tc.value)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error does not point at %s: %v", tc.says, err)
			}
		})
	}
}

// A vendor's values are required only once a selector names it. That conditional is the one thing
// between a local stack and needing an SMTP account, an SMS contract and a KYC subscription — and a
// selector naming a vendor whose values are blank has to fail, because a deployment that thinks it
// sends real email and does not is discovered by the user who never got their reset link.
func TestLoad_VendorValuesFollowTheirSelector(t *testing.T) {
	cases := []struct {
		name     string
		selector string
		vendor   string
		filled   map[string]any
	}{
		{
			name: "smtp", selector: "email.provider", vendor: "smtp",
			filled: map[string]any{
				"email.smtp_host": "smtp.example", "email.smtp_port": 587,
				"email.smtp_username": "u", "email.smtp_password": "p",
				"email.from":               "ShopNexus <no-reply@shopnexus.test>",
				"email.timeout":            "10s",
				"email.verify_url":         "https://shopnexus.test/verify",
				"email.reset_password_url": "https://shopnexus.test/reset",
				"email.app_base_url":       "https://shopnexus.test",
			},
		},
		{
			name: "esms", selector: "sms.provider", vendor: "esms",
			filled: map[string]any{
				"sms.esms_base_url": "https://rest.esms.vn", "sms.esms_api_key": "k",
				"sms.esms_secret_key": "s", "sms.esms_brandname": "ShopNexus",
				"sms.esms_sms_type": "2", "sms.esms_content_template": "{{.Code}}",
				"sms.esms_timeout": "10s",
			},
		},
		{
			name: "fpt-ai", selector: "kyc.provider", vendor: "fpt-ai",
			filled: map[string]any{
				"kyc.base_url": "https://api.fpt.ai", "kyc.api_key": "k",
				"kyc.request_timeout": "30s", "kyc.download_timeout": "30s",
			},
		},
		{
			name: "bge-m3", selector: "embedding.provider", vendor: "bge-m3",
			filled: map[string]any{
				"embedding.base_url": "http://embedding:5007", "embedding.api_key": "k",
				"embedding.timeout": "5m",
			},
		},
		{
			name: "litellm", selector: "llm.provider", vendor: "litellm",
			filled: map[string]any{
				"llm.base_url": "http://litellm:4000", "llm.api_key": "k",
				"llm.chat_model": "gpt-4o-mini", "llm.transcribe_model": "whisper-1",
				"llm.request_timeout": "45s", "llm.stream_timeout": "5m",
			},
		},
		{
			name: "restate", selector: "workflow.runtime", vendor: "restate",
			filled: map[string]any{
				"workflow.serve_addr":   "0.0.0.0:9080",
				"workflow.ingress_url":  "http://restate:8080",
				"workflow.send_timeout": "5s",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+" selected without its values fails", func(t *testing.T) {
			doc := document(t)
			set(t, doc, tc.selector, tc.vendor)
			for path := range tc.filled {
				blank(t, doc, path)
			}
			if _, err := load(t, doc); err == nil {
				t.Fatalf("%s = %q loaded with its values blank", tc.selector, tc.vendor)
			}
		})

		t.Run(tc.name+" selected with its values loads", func(t *testing.T) {
			doc := document(t)
			set(t, doc, tc.selector, tc.vendor)
			for path, value := range tc.filled {
				set(t, doc, path, value)
			}
			if _, err := load(t, doc); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	// The other direction, once: with every selector on `mock`, nothing vendor-shaped is needed — or a
	// checkout could not be run without somebody's contract.
	t.Run("all mocks need no credentials", func(t *testing.T) {
		if _, err := load(t, document(t)); err != nil {
			t.Fatalf("a mock-only deployment must need no vendor values: %v", err)
		}
	})
}

// A payment rail is registered by naming it in a *list*, which no `validate` tag can ask about — so
// the rule is a struct-level validation, and that is exactly why it is worth a test.
func TestLoad_ANamedRailNeedsItsCredentials(t *testing.T) {
	t.Run("sepay named without its keys fails", func(t *testing.T) {
		doc := document(t)
		set(t, doc, "payment.providers", []any{"mock", "sepay"})

		_, err := load(t, doc)
		if err == nil {
			t.Fatal("sepay is registered with no merchant id")
		}
		if !strings.Contains(err.Error(), "sepay") {
			t.Errorf("error does not name the section to fix: %v", err)
		}
	})

	t.Run("sepay named with its keys loads", func(t *testing.T) {
		doc := document(t)
		set(t, doc, "payment.providers", []any{"mock", "sepay"})
		set(t, doc, "payment.sepay.merchant_id", "merchant-1")
		set(t, doc, "payment.sepay.secret_key", "sign-secret")
		set(t, doc, "payment.sepay.ipn_secret_key", "ipn-secret")

		cfg, err := load(t, doc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.PaymentProviders) != 2 {
			t.Fatalf("providers = %v, want both", cfg.PaymentProviders)
		}
	})

	t.Run("stripe named without its timeout fails", func(t *testing.T) {
		doc := document(t)
		set(t, doc, "payment.providers", []any{"stripe"})
		set(t, doc, "payment.stripe.secret_key", "sk_test_x")
		set(t, doc, "payment.stripe.webhook_secret", "whsec_x")
		blank(t, doc, "payment.stripe.request_timeout")

		if _, err := load(t, doc); err == nil {
			t.Fatal("stripe is registered with no request timeout")
		}
	})

	// A rail with no implementation cannot be registered, however complete its credentials look.
	t.Run("a rail this binary does not have fails", func(t *testing.T) {
		doc := document(t)
		set(t, doc, "payment.providers", []any{"vnpay"})

		if _, err := load(t, doc); err == nil {
			t.Fatal("a provider with no implementation loaded")
		}
	})
}

// The path is the one thing left to the environment, so a wrong one has to say what it looked for and
// how to point it somewhere else.
func TestLoad_AMissingFileNamesItself(t *testing.T) {
	t.Setenv(config.FileVar, filepath.Join(t.TempDir(), "nope.yml"))

	_, err := config.Load(validation.Default())
	if err == nil {
		t.Fatal("a missing document loaded")
	}
	if !strings.Contains(err.Error(), "nope.yml") || !strings.Contains(err.Error(), config.FileVar) {
		t.Errorf("error names neither the path nor how to change it: %v", err)
	}
}
