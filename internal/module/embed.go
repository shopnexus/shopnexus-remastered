// Package module embeds every module's SQL migration tree so cmd/migrate is a
// self-contained binary: it runs inside the distroless image / k8s migration
// Job without shipping loose .sql files. Each module's migrations live under
// the sub-path "<module>/db/migrations".
package module

import "embed"

//go:embed all:account/db/migrations all:analytic/db/migrations all:catalog/db/migrations all:chat/db/migrations all:common/db/migrations all:inventory/db/migrations all:order/db/migrations all:promotion/db/migrations
var Migrations embed.FS
