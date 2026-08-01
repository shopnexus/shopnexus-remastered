package main

import (
	"testing"

	"go.uber.org/fx"
)

// The graph is built but no constructor runs, so this needs no database, no Redis and no env —
// which is the point: a duplicate provide, a missing dependency or a mistyped group tag is a
// startup failure, and until this test existed the only thing that caught one was starting the
// process. Two have been found that way: a bare *pgxpool.Pool provided by both order and finance,
// and later a bare *uploads.Store provided by every module that takes an upload.
func TestAppGraph_IsValid(t *testing.T) {
	if err := fx.ValidateApp(appOptions()); err != nil {
		t.Fatalf("the application graph cannot be built: %v", err)
	}
}
