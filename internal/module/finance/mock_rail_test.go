package finance_test

import (
	"io/fs"
	"strings"
	"testing"

	"shopnexus/internal/module/finance"
	paymentmock "shopnexus/internal/provider/payment/mock"
)

// The mock rail's behaviour lives in Go and the rows a client picks from live in SQL, so the two
// can drift: a row nobody implements silently succeeds, and a scenario nobody seeded is
// unreachable. Neither shows up as a failure anywhere else — a checkout just does the wrong thing.
func TestMockPaymentOptions_EveryScenarioIsSeededAndNothingElseIs(t *testing.T) {
	sql := readMigrations(t, finance.Migrations())

	for _, slug := range paymentmock.ScenarioIDs() {
		if !strings.Contains(sql, "'"+slug+"'") {
			t.Errorf("scenario %q is implemented but no migration seeds it, so nobody can pick it", slug)
		}
	}
	// The other direction: a `mock-` row in the SQL that the rail does not know falls back to
	// succeeding, so "decline this payment" would quietly take the money.
	for _, slug := range mockSlugsIn(sql) {
		if !contains(paymentmock.ScenarioIDs(), slug) {
			t.Errorf("migration seeds %q but the rail does not implement it, so it silently succeeds", slug)
		}
	}
}

func readMigrations(t *testing.T, dir fs.FS) string {
	t.Helper()
	var b strings.Builder
	err := fs.WalkDir(dir, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(dir, path)
		if err != nil {
			return err
		}
		b.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	return b.String()
}

// mockSlugsIn picks the quoted `mock-…` literals out of the SQL. Crude on purpose: the alternative
// is parsing SQL, and the only quoted mock- literals in a migration are these ids.
func mockSlugsIn(sql string) []string {
	var out []string
	for rest := sql; ; {
		i := strings.Index(rest, "'mock-")
		if i < 0 {
			return out
		}
		rest = rest[i+1:]
		end := strings.IndexByte(rest, '\'')
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
		rest = rest[end:]
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
