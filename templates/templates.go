// Package templates carries the copy this API sends out: the transactional mail rendered
// by internal/provider/notify/smtp, and the notification feed's wording rendered by the
// account module's copybook.
//
// It sits at the repository root beside `api` rather than under the package that
// renders it, for one hard reason and one soft one. The hard one: a `go:embed` pattern
// cannot name a parent directory, so nothing under internal/ can reach a folder here,
// and a copy of the files beside each sender is a copy that drifts. The soft one: copy
// is edited by people who do not read Go, and asking them to find it six directories
// down is asking for it to be edited in the wrong place.
package templates

import (
	"embed"
	"io/fs"
)

//go:embed mail/*.html
var mailFS embed.FS

//go:embed notification/*.yaml
var notificationFS embed.FS

// Mail returns the transactional mail templates, rooted at the mail directory so a
// caller names "order-placed.vi.html" instead of repeating the folder.
//
// Embedded rather than read from disk: the runtime image is distroless and carries
// nothing but the binary, and a deployment that forgot to copy a directory would fail
// at the first order rather than at startup.
func Mail() fs.FS {
	sub, err := fs.Sub(mailFS, "mail")
	if err != nil {
		panic(err)
	}
	return sub
}

// Notification returns the feed's copybook, one file per language, rooted at the
// notification directory so a caller names "vi.yaml".
//
// A file per language rather than per kind — unlike mail, which is a page of markup each.
// A feed row is a title and one supporting line, so the whole vocabulary of one language
// fits on a screen, which is where a translator wants it.
func Notification() fs.FS {
	sub, err := fs.Sub(notificationFS, "notification")
	if err != nil {
		panic(err)
	}
	return sub
}
