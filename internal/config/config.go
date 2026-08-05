// Package config loads the configuration document and validates it, failing fast at startup.
package config

import "time"

// Config is what the program reads: one flat value, assembled from the grouped document in
// `file.go`, which is also where every rule about a value lives. Nothing here has a default and
// nothing is optional in the sense of "carry on without it" — a deployment that is missing a value
// does not start.
//
// The one conditional is a vendor's credentials, required only when a seam's selector picked that
// vendor, or when a provider list names it. Without that, running the stack locally would need an
// SMTP account, an SMS contract and a KYC subscription.
// The WORKFLOW_RUNTIME values this binary understands. Named here, beside the field that carries
// them, because the composition root and every module's own selector compare against the same
// two strings — the `oneof` tag above is the one place a literal is unavoidable.
const (
	WorkflowRestate = "restate"
	WorkflowOff     = "off"
)

type Config struct {
	GatewayAddr string
	// InstanceID tags every telemetry row with the pod/host that produced it;
	// without it several replicas collapse into one meaningless series. Required
	// rather than defaulted to the hostname: a wrong-but-plausible value is worse
	// than a startup failure. In Kubernetes use the downward API (metadata.name).
	InstanceID   string
	AccountDBDSN string
	CatalogDBDSN string
	OrderDBDSN   string
	ChatDBDSN    string
	TrustDBDSN   string
	FinanceDBDSN string
	// ObservabilityDBDSN backs the observability schema (product events + metrics hypertables).
	ObservabilityDBDSN string
	// NATSURL is the JetStream bus that buffers telemetry between the Sink and
	// the writer (e.g. nats://nats:4222).
	NATSURL       string
	RedisAddr     string
	RedisPassword string
	JWTSecret     string
	// IDCipherKey keys the permutation behind every opaque id (shared/id). 16, 24
	// or 32 bytes — id.SetCipher owns that rule, so it is not repeated here.
	// Rotating it invalidates every id ever handed out: back it up like a DB
	// credential.
	IDCipherKey string
	LogLevel    string
	// CORSAllowedOrigins is where a browser may call this API from: full origins
	// (`https://shopnexus.github.io`), comma-separated, or a lone `*`. Full origins rather than
	// hosts like WSAllowedOrigins, because that is the form the browser sends and the form the
	// allow header has to echo. Required like everything else here: a deployment whose web client
	// is on another origin and whose list is empty answers every preflight with a refusal, which
	// surfaces as "the whole site is broken" rather than as a missing variable.
	CORSAllowedOrigins []string

	// StorageProvider is where a *new* uploaded byte goes. `local` keeps objects on this host's
	// disk and signs URLs back to the gateway's own upload route — a real store signs the
	// bucket directly and the bytes never touch this process. Same rule as the other seams: a
	// selector, not a default, because a deployment that thinks photos are in a bucket and
	// finds them on a pod's disk discovers it the first time the pod is replaced.
	//
	// It does not decide what can be *read*. Every `resource` row records the store holding it,
	// and storage.Registry keeps them all reachable, so changing this moves where the next
	// upload lands without stranding a single object that predates the change.
	StorageProvider string
	// StorageRoot is the directory `local` stores objects under.
	StorageRoot string
	// StorageSecret keys the signature on an upload slot. Its own secret, not the JWT's: this
	// one signs a URL that sits in a client's memory, and rotating it invalidates only the
	// slots still in flight.
	StorageSecret string
	// StorageUploadTTL is how long a slot stays writable, StorageDownloadTTL how long a read
	// link lives. Both short: a link that outlives the page it was rendered for is a hole.
	StorageUploadTTL   time.Duration
	StorageDownloadTTL time.Duration
	// StorageMaxUploadBytes is the largest object accepted, refused before a byte moves.
	// StorageMaxVideoBytes is the same for a `video/*` type: one limit for both would have to be
	// the video's, and a 100 MB avatar or PDF is then a slot the platform happily signs.
	StorageMaxUploadBytes int64
	StorageMaxVideoBytes  int64
	// StorageAllowedMimes is what may be stored at all. An allowlist: a store that accepts
	// anything serves anything back, and `text/html` from your own domain is a stored script.
	StorageAllowedMimes []string

	// --- embedding (cmd/embedder) ---

	// EmbeddingProvider picks the model behind the vectors search ranks on. `mock` hashes the
	// words instead, which exercises the whole queue-to-column path without the multi-gigabyte
	// download — a stack that cannot run without a GPU is a stack nobody runs locally. Same
	// rule as the other seams: a selector, not a default.
	EmbeddingProvider string
	EmbeddingBaseURL  string
	// EmbeddingAPIKey is the model service's bearer token. Required with the real provider like
	// every other vendor credential: the service holds a GPU and answers anyone who can reach
	// it, so an unauthenticated deployment is one somebody else can spend.
	EmbeddingAPIKey string
	// EmbeddingTimeout bounds one batch. Generous next to the other providers: this is a
	// transformer over a batch of documents, often on a CPU.
	EmbeddingTimeout time.Duration
	// EmbeddingDimensions is the dense width, and it must equal what
	// `catalog.listing_embedding.dense` was declared with — the model service lives in another
	// repository, so nothing but this check couples the two. Every answer is measured against
	// it, because a model of the wrong size does not degrade: every row fails until one of the
	// two changes, and a migration is the other half of changing it.
	EmbeddingDimensions int
	// EmbeddingInterval is how often the queues are drained. One pass empties them, so this is
	// how long a new listing waits to become searchable, not how fast a backlog is worked off.
	EmbeddingInterval time.Duration
	// EmbeddingBatchSize is rows per model call, which also bounds the write transaction and
	// how much a crash repeats.
	EmbeddingBatchSize int
	// EmbeddingMaxTextChars clips what the model reads. The sparse vector has one non-zero per
	// distinct token and the HNSW index refuses more than a thousand, so an unbounded
	// description is a failed write rather than a poor result.
	EmbeddingMaxTextChars int

	// --- listing suggestions (the "photo in, listing out" route) ---

	// LLMProvider picks who reads a seller's photo and voice note. `mock` answers a plausible
	// filled form with no model, no key and no network call, so a local stack walks the whole
	// flow — same rule as the other seams: a selector, not a default, because a deployment that
	// thinks it has a model and does not is discovered by the seller whose form stayed empty.
	LLMProvider string
	// The proxy every model is reached through. One base URL and one key, because chat and
	// transcription are the same vendor account.
	LiteLLMBaseURL string
	LiteLLMAPIKey  string
	// LiteLLMChatModel has to be one that reads images: the photo is the input a seller cares
	// about, and a text-only model answers a form filled from the note alone.
	LiteLLMChatModel       string
	LiteLLMTranscribeModel string
	LiteLLMRequestTimeout  time.Duration
	// LiteLLMStreamTimeout covers a whole streamed generation. Nothing streams today; it is
	// required because the client refuses to be built without one, for the reason its own
	// comment gives.
	LiteLLMStreamTimeout time.Duration

	// --- durable execution ---

	// WorkflowRuntime picks who holds the timers this marketplace waits on — an unpaid
	// checkout expiring, an escrow window closing, a refund deadline passing. `restate`
	// starts a run per entity and signals it; `off` leaves every transition to the sweep,
	// which is the same code on a slower clock. Same rule as the provider seams: a selector,
	// not a default, so a deployment that thinks it has durable timers and does not is found
	// at startup rather than by the seller who was never paid.
	WorkflowRuntime string
	// RestateServeAddr is where the SDK serves the workflow handlers for the runtime to
	// invoke; RestateIngressURL is where this process submits and signals runs.
	RestateServeAddr   string
	RestateIngressURL  string
	RestateSendTimeout time.Duration
	// SweepInterval is how often the timed transitions are swept. Required even with
	// Restate: the sweep is the net under a lost run, and with a runtime in place it finds
	// nothing, which is what makes it cheap to leave on.
	SweepInterval time.Duration

	// --- outbound providers: which implementation each seam gets ---

	// EmailProvider and SMSProvider are separate because no vendor is good at both:
	// "smtp" against any relay, "esms" for the Vietnamese brandname aggregator, "mock"
	// to log what would have been sent.
	EmailProvider string
	SMSProvider   string
	// OAuthVerifier is "oidc" to verify a real id token, or "mock" to trust the
	// credential in dev.
	OAuthVerifier string
	// KYCProvider is "fpt-ai" for the real check, or "mock" to leave every case pending
	// for a moderator.
	KYCProvider string
	// PaymentReturnURLHosts is where a payment gateway may send a payer back. An allowlist
	// rather than any URL the client sends: a redirect target nobody checked is an open
	// redirect wearing a payment flow, and the platform's own domain is what lends it
	// credibility. Comma-separated hosts, no scheme.
	PaymentReturnURLHosts []string
	// PaymentProviders and TransportProviders are the implementations this deployment registers —
	// a list, not a selector, because *the option row* names which one serves it. That is what lets
	// two rails be live at once and an admin move a carrier from GHN to GHTK without a restart.
	//
	// A provider left out is one no row can name, which is the point: `mock` beside a real rail is
	// safe (nothing charges through it unless a row asks for it) but leaving it out of a deployment
	// that takes real money means such a row cannot even be written by hand. A provider that
	// declares its own rows also *removes* them when it stops being listed, at the next start.
	//
	// Each named provider's credentials are required — checked in Load rather than by a tag, since
	// `required_if` cannot ask whether a list contains a value. Same rule as the vendor seams
	// above: not a default, and a startup failure rather than a rail that quietly cannot charge.
	PaymentProviders   []string
	TransportProviders []string
	// PublicBaseURL is where this gateway answers as a client sees it, API base path included
	// (`https://shopnexus.example/api/v1`) — behind a proxy that is not the address the server
	// binds. One var rather than one per seam, because every user of it needs the same answer:
	// `local` storage signs upload and download URLs back to this gateway's own routes, and a
	// redirect rail hands a browser an absolute link to a page this process serves. A web client on
	// another origin cannot follow a relative one.
	PublicBaseURL string

	// --- SePay (bank transfer) ---

	// SePaySandbox picks the test host. Its own var and not inferred from the key, because a
	// deployment that thinks it is taking real transfers and is not would be discovered by the
	// seller who was never paid.
	SePayMerchantID string
	SePaySecretKey  string
	// SePayIPNSecretKey authenticates SePay's callback to us; SePaySecretKey signs our checkout to
	// them. Two secrets, opposite directions.
	SePayIPNSecretKey string
	SePaySandbox      bool

	// --- Stripe (cards) ---

	StripeSecretKey string
	// StripeWebhookSecret verifies the callback signature. Without it the webhook is an endpoint
	// anybody can use to mark an order paid.
	StripeWebhookSecret  string
	StripeRequestTimeout time.Duration

	// --- SMTP ---

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	// SMTPFrom is the header and envelope sender, e.g. "ShopNexus <no-reply@shopnexus.vn>".
	SMTPFrom    string
	SMTPTimeout time.Duration
	// VerifyEmailURL and ResetPasswordURL are the *client's* pages; the API appends the
	// token as a query parameter. They cannot be derived from a request path, because the
	// API does not serve those pages.
	VerifyEmailURL   string
	ResetPasswordURL string

	// --- eSMS.vn ---

	ESMSBaseURL   string
	ESMSAPIKey    string
	ESMSSecretKey string
	// ESMSBrandname is the registered sender name; an unregistered one is rejected by
	// the aggregator.
	ESMSBrandname string
	// ESMSSMSType is eSMS's message class, which follows from the contract — customer
	// care and advertising traffic are priced and routed differently.
	ESMSSMSType string
	// ESMSContentTemplate must match the template registered with the carriers. It is
	// given {{.Code}}.
	ESMSContentTemplate string
	// ESMSUnicode doubles the cost per segment, so leave it false for a template written
	// without diacritics.
	ESMSUnicode bool
	// ESMSSandbox has the aggregator accept and drop the message: for staging, which
	// should not spend credit or ring a real phone.
	ESMSSandbox bool
	ESMSTimeout time.Duration

	// --- OIDC ---

	// OIDCGoogleAudiences and OIDCAppleAudiences are comma-separated client ids. A
	// provider with no audience configured is simply not offered; at least one of them
	// has to be set when the verifier is "oidc", which the verifier itself enforces.
	OIDCGoogleAudiences []string
	OIDCAppleAudiences  []string
	OIDCTimeout         time.Duration

	// --- FPT.AI eKYC ---

	FPTAIBaseURL string
	FPTAIAPIKey  string
	// FPTAIRequestTimeout bounds one vendor call, FPTAIDownloadTimeout one scan fetch
	// from storage. Separate because they are different dependencies.
	FPTAIRequestTimeout  time.Duration
	FPTAIDownloadTimeout time.Duration

	// --- WebSocket realtime ---

	// WSTicketTTL is how long a handshake ticket lives before it is useless — short,
	// because it only has to survive the moment between minting it and opening the socket.
	WSTicketTTL time.Duration
	// WSWriteTimeout bounds one write and one ping, same rule as every other outbound
	// deadline here: a socket write that never completes must not hang the pump forever.
	WSWriteTimeout time.Duration
	// WSPingInterval is how often an idle socket is probed, so a peer that vanished
	// without a FIN is found by a failed pong instead of an accumulating goroutine.
	WSPingInterval time.Duration
	// WSSendBuffer is how many envelopes a socket may fall behind before the hub drops it.
	WSSendBuffer int
	// WSMaxPerAccount caps concurrent sockets per account, so a tab-spammer cannot exhaust
	// the process's file descriptors.
	WSMaxPerAccount int
	// WSAllowedOrigins is matched against the Origin header's host, not a full URL —
	// that is what coder/websocket's OriginPatterns compares against.
	WSAllowedOrigins []string
}
