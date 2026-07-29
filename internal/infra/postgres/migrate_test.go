package postgres

import (
	"testing"
	"testing/fstest"
)

func TestReadMigrations_SortedAndFiltered(t *testing.T) {
	fsys := fstest.MapFS{
		"002_second.sql": {Data: []byte("SELECT 2;")},
		"001_first.sql":  {Data: []byte("SELECT 1;")},
		"notes.txt":      {Data: []byte("ignore me")},
	}
	got, err := readMigrations(fsys)
	if err != nil {
		t.Fatalf("readMigrations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (only .sql)", len(got))
	}
	if got[0].name != "001_first.sql" || got[1].name != "002_second.sql" {
		t.Fatalf("wrong order: %+v", got)
	}
}
