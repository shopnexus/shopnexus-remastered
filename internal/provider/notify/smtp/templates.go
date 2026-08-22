package smtp

import (
	"fmt"
	"html/template"
	"io/fs"

	"shopnexus/internal/provider/notify"
	"shopnexus/internal/shared/lang"
)

// The transactional mail this API sends, loaded from the templates/mail directory at the
// repository root (see package templates for why the files live there).
//
// One file per kind per language, each defining "subject", "title", "lead" and "action",
// optionally overriding "footer" and "extra". The frame — the layout, the button, the
// fallback link, the automated-mail notice — is a file of its own per language, so adding
// a mail is writing copy and never copying markup.

// mailKinds is every kind this package has copy for.
//
// A list in code rather than a walk of the directory, in both directions: a template file
// nobody named here is dead weight nothing sends, and a Kind named here with no file is a
// mail that would fail at 3am — so loadMails refuses to start instead.
var mailKinds = []notify.Kind{
	notify.KindEmailVerification,
	notify.KindPasswordReset,
	notify.KindOrderPlaced,
	notify.KindOrderReceived,
	notify.KindOrderDelivered,
	notify.KindOrderCompleted,
	notify.KindOrderCancelled,
	notify.KindRefundResolved,
	notify.KindOrderUnconfirmed,
}

// blocks every mail file has to define. The frame defaults "footer", "extra" and
// "escrow_state". Preheader and badge are required, not defaulted: both are read before the
// mail is opened, and a shared default there makes every mail look like the last one.
var requiredBlocks = [...]string{"subject", "preheader", "badge", "title", "lead", "action"}

// tone is the palette one mail is drawn in. Three hex values rather than a name, because
// html/template cannot branch on a string. The four tones are the website's own
// (src/lib/order-state.ts), so a mail and the page it links to agree on what green means.
type tone struct {
	Ink  string // text on Wash, and the state word
	Wash string // the badge and escrow box fill
	Rule string // the 2px line under the header
}

var (
	toneSuccess = tone{Ink: "#00504a", Wash: "#e6f0ef", Rule: "#00685f"}
	toneMoving  = tone{Ink: "#266d67", Wash: "#a8ece4", Rule: "#266d67"}
	toneWaiting = tone{Ink: "#924628", Wash: "#ffdbcf", Rule: "#924628"}
	toneDanger  = tone{Ink: "#93000a", Wash: "#ffdad6", Rule: "#ba1a1a"}
)

// railBeats is how many segments the order rail has. The labels are copy and live in each
// frame; the count lives here so the two frames cannot disagree.
const railBeats = 4

// look is a mail's appearance where it follows from the kind rather than the wording. Here
// rather than in the templates for the same reason the link is: a fact that renders
// differently per language is a bug waiting for the reader who gets both.
type look struct {
	tone tone
	// step is the beat this mail reports, 1..railBeats. Zero draws no rail: for the two
	// non-order mails, and for branches that end the sequence rather than advance it.
	step int
	// escrow draws the escrow box. Every order mail does: where the buyer's money is sitting
	// is the question this marketplace exists to answer.
	escrow bool
}

var looks = map[notify.Kind]look{
	notify.KindEmailVerification: {tone: toneMoving},
	notify.KindPasswordReset:     {tone: toneWaiting},
	notify.KindOrderPlaced:       {tone: toneMoving, step: 1, escrow: true},
	notify.KindOrderReceived:     {tone: toneMoving, step: 1, escrow: true},
	notify.KindOrderDelivered:    {tone: toneWaiting, step: 3, escrow: true},
	notify.KindOrderCompleted:    {tone: toneSuccess, step: railBeats, escrow: true},
	// No rail below: all three would point at a beat that will not arrive.
	notify.KindOrderCancelled: {tone: toneDanger, escrow: true},
	// Waiting, not danger: red would tell one of the two recipients they lost before reading.
	notify.KindRefundResolved:   {tone: toneWaiting, escrow: true},
	notify.KindOrderUnconfirmed: {tone: toneWaiting, escrow: true},
}

// mailData is what a template is executed against. Params is reached as `.Params.order_id`,
// and a key the caller did not send is a render failure — see missingkey below.
type mailData struct {
	Lang   string
	Link   string
	Params map[string]any

	// Tone, Step and Escrow come from looks, and the frame draws itself from them.
	Tone   tone
	Step   int
	Escrow bool

	// Amount is the escrowed sum, grouped for this language, empty when no total was sent.
	// Computed here because missingkey=error leaves a template unable to ask.
	Amount string
	// OrderRef sits beside the wordmark, empty for the two account mails.
	OrderRef string
	// HelpLink is the help centre, for the recipient the mail did not answer.
	HelpLink string
}

// mail is one kind in one language: the parsed set, plus the name of the frame to execute
// for the body. The subject is a named block in the same set.
type mail struct {
	set   *template.Template
	frame string
}

// loadMails parses every kind × language at startup, so a missing file, an unparseable
// template or a mail that forgot to define its subject is a process that does not come up
// — rather than a send that fails on the one night it matters.
func loadMails(fsys fs.FS) (map[notify.Kind]map[string]*mail, error) {
	out := make(map[notify.Kind]map[string]*mail, len(mailKinds))
	for _, kind := range mailKinds {
		byLang := make(map[string]*mail, len(lang.All))
		for _, l := range lang.All {
			m, err := loadMail(fsys, kind, l)
			if err != nil {
				return nil, err
			}
			byLang[l] = m
		}
		out[kind] = byLang
	}
	return out, nil
}

func loadMail(fsys fs.FS, kind notify.Kind, l string) (*mail, error) {
	frame := "frame." + l + ".html"
	file := string(kind) + "." + l + ".html"

	// Two Parse calls rather than one with both files: redefinition across successive
	// parses is defined behaviour, and it is what lets a mail override the frame's default
	// "footer". Parsing them together leaves which definition wins up to argument order.
	set, err := template.New(frame).Funcs(lang.Funcs(l)).Option("missingkey=error").ParseFS(fsys, frame)
	if err != nil {
		return nil, fmt.Errorf("parse mail frame %s: %w", frame, err)
	}
	if set, err = set.ParseFS(fsys, file); err != nil {
		return nil, fmt.Errorf("parse mail template %s: %w", file, err)
	}
	for _, block := range requiredBlocks {
		if set.Lookup(block) == nil {
			return nil, fmt.Errorf("mail template %s does not define %q", file, block)
		}
	}
	return &mail{set: set, frame: frame}, nil
}
