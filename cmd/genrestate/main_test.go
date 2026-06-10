package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func generate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile("testdata/svc/iface.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "iface.go"), src, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restate_gen.go")
	cmd := exec.Command("go", "run", ".",
		"-src", filepath.Join(dir, "iface.go"),
		"-interface", "SvcBiz", "-service", "Svc", "-out", out)
	cmd.Dir = "."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("genrestate failed: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The Restate service adapter delegates every interface method, each with its
// real ctx type: GetThing (query) and DoThing (command) are both restate.Context
// handlers for restate.Reflect binding.
func TestEmitsServiceAdapter(t *testing.T) {
	got := generate(t)
	for _, want := range []string{
		"type SvcService struct {",
		"biz SvcBiz",
		"func NewSvcService(biz SvcBiz) *SvcService",
		"func (s *SvcService) ServiceName() string { return serviceName }",
		"func (s *SvcService) GetThing(ctx restate.Context, id int64) (string, error) {",
		"return s.biz.GetThing(ctx, id)",
		"func (s *SvcService) DoThing(ctx restate.Context, id int64) error {",
		"return s.biz.DoThing(ctx, id)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated output missing %q\n---\n%s", want, got)
		}
	}
}

// Command surfaces: DoThing (restate.Context) lands on Call/Future/Sender.
func TestEmitsCommandSurfaces(t *testing.T) {
	got := generate(t)
	for _, want := range []string{
		// Call interface = command request-response, restate.Context.
		"type SvcBizCall interface {",
		"DoThing(ctx restate.Context, id int64) error",
		// Sender / Future over commands, restate.Context.
		"type SvcBizSender interface {",
		"type SvcBizFuture interface {",
		"DoThing(rctx restate.Context, id int64) restate.ResponseFuture[restate.Void]",
		// Restate proxy carries the command call methods.
		"func (p *SvcRestateCall) DoThing(ctx restate.Context, id int64) error {",
		"restatec.CallVoid(ctx, p.call, serviceName, \"DoThing\", id)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated output missing %q\n---\n%s", want, got)
		}
	}
	// Commands must NOT appear as flat query methods.
	if strings.Contains(got, "func (c *svcBizClient) DoThing(") {
		t.Errorf("command DoThing leaked onto flat client surface\n---\n%s", got)
	}
}

// Flat query surface: GetThing (context.Context) inline on the client, with
// in-process + remote besteffort impls; commands are excluded from besteffort.
func TestEmitsFlatQueryClient(t *testing.T) {
	got := generate(t)
	for _, want := range []string{
		// Unified client interface: flat query inline + Call/Future/Send selectors.
		"type SvcBizClient interface {",
		"GetThing(ctx context.Context, id int64) (string, error)",
		"Call() SvcBizCall",
		"Future() SvcBizFuture",
		"Send() SvcBizSender",
		// Flat impl interface + in-process local.
		"type svcBizBestEffortLocal struct{ biz SvcBiz }",
		"func (b *svcBizBestEffortLocal) GetThing(ctx context.Context, id int64) (string, error) {",
		"return b.biz.GetThing(ctx, id)",
		// HTTP/2 remote impl.
		"type svcBizBestEffortRemote struct{ call *besteffort.CallClient }",
		"func (b *svcBizBestEffortRemote) GetThing(ctx context.Context, id int64) (string, error) {",
		"return besteffort.Call[string](ctx, b.call, serviceName, \"GetThing\", id)",
		// Unified client struct + flat delegation + selectors.
		"type svcBizClient struct {",
		"func (c *svcBizClient) GetThing(ctx context.Context, id int64) (string, error) {",
		"func (c *svcBizClient) Call() SvcBizCall {",
		"func (c *svcBizClient) Future() SvcBizFuture {",
		"func (c *svcBizClient) Send() SvcBizSender {",
		"func NewSvcBizClientInProcess(restateIngressURL string, biz SvcBiz) SvcBizClient {",
		"func NewSvcBizClientRemote(restateIngressURL, bestEffortURL string) SvcBizClient {",
		// BestEffort server registration: GetThing only, never DoThing.
		"func RegisterSvcBestEffort(s *besteffort.Server, biz SvcBiz) {",
		"s.Handle(serviceName, \"GetThing\", func(ctx context.Context, body []byte) (any, error) {",
		"return biz.GetThing(ctx, p)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated output missing %q\n---\n%s", want, got)
		}
	}
	// DoThing is a command — never served by the besteffort HTTP server.
	if strings.Contains(got, `s.Handle(serviceName, "DoThing"`) {
		t.Errorf("command DoThing registered on besteffort server\n---\n%s", got)
	}
	if strings.Contains(got, "func (b *svcBizBestEffortLocal) DoThing(") {
		t.Errorf("command DoThing leaked onto besteffort local impl\n---\n%s", got)
	}
}
