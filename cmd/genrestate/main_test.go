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
