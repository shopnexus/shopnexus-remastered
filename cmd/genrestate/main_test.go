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

func TestEmitsGuaranteedAdapter(t *testing.T) {
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

func TestEmitsGuaranteedBestEffortClient(t *testing.T) {
	got := generate(t)
	for _, want := range []string{
		// Guaranteed surface
		"type SvcBizGuaranteed interface {",
		"Send() SvcBizSender",
		"Future() SvcBizFuture",
		// BestEffort surface
		"type SvcBizBestEffort interface {",
		// In-process local impl
		"type svcBizBestEffortLocal struct{ biz SvcBiz }",
		"func (b *svcBizBestEffortLocal) GetThing(ctx context.Context, id int64) (string, error) {",
		"return b.biz.GetThing(ctx, id)",
		"func (b *svcBizBestEffortLocal) DoThing(ctx context.Context, id int64) error {",
		"return b.biz.DoThing(ctx, id)",
		// HTTP/2 remote impl
		"type svcBizBestEffortRemote struct{ call *besteffort.CallClient }",
		"func (b *svcBizBestEffortRemote) GetThing(ctx context.Context, id int64) (string, error) {",
		"return besteffort.Call[string](ctx, b.call, serviceName, \"GetThing\", id)",
		"func (b *svcBizBestEffortRemote) DoThing(ctx context.Context, id int64) error {",
		"return besteffort.CallVoid(ctx, b.call, serviceName, \"DoThing\", id)",
		// Unified client interface exposes only the two transport selectors
		"type SvcBizClient interface {",
		"Guaranteed() SvcBizGuaranteed",
		"BestEffort() SvcBizBestEffort",
		// Unified client struct + constructors
		"type svcBizClient struct {",
		"*SvcRestateClient",
		"bestEffort SvcBizBestEffort",
		"func (c *svcBizClient) Guaranteed() SvcBizGuaranteed { return c.SvcRestateClient }",
		"func (c *svcBizClient) BestEffort() SvcBizBestEffort { return c.bestEffort }",
		"func NewSvcBizClientInProcess(restateIngressURL string, biz SvcBiz) SvcBizClient {",
		"func NewSvcBizClientRemote(restateIngressURL, bestEffortURL string) SvcBizClient {",
		// BestEffort server registration
		"func RegisterSvcBestEffort(s *besteffort.Server, biz SvcBiz) {",
		"s.Handle(serviceName, \"GetThing\", func(ctx context.Context, body []byte) (any, error) {",
		"return biz.GetThing(ctx, p)",
		"return nil, biz.DoThing(ctx, p)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated output missing %q\n---\n%s", want, got)
		}
	}
}
