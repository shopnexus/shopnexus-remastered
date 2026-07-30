package smtp

import (
	"html/template"

	"shopnexus/internal/provider/notify"
)

// The two transactional emails this API sends, in the two languages it serves. They
// live in Go rather than in a CMS because the wording is part of a security flow: a
// reset mail that does not say what to do when you did not ask for one is a phishing
// lesson waiting to happen.
//
// Each is a single HTML part. A multipart/alternative message would be nicer for a
// text-only reader, and is not worth the machinery for two mails whose whole content
// is one sentence and one link.

type mail struct {
	Subject string
	Body    *template.Template
}

// body is the shared frame, so a new mail is a title and a paragraph rather than a
// copy of the layout.
const bodyFrame = `<!doctype html>
<html lang="{{.Lang}}">
<body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;line-height:1.5;color:#111">
  <h2 style="margin:0 0 12px">{{.Title}}</h2>
  <p style="margin:0 0 16px">{{.Lead}}</p>
  <p style="margin:0 0 24px">
    <a href="{{.Link}}" style="display:inline-block;padding:10px 18px;background:#111;color:#fff;border-radius:6px;text-decoration:none">{{.Action}}</a>
  </p>
  <p style="margin:0 0 8px;font-size:13px;color:#555">{{.Fallback}}<br><span style="word-break:break-all">{{.Link}}</span></p>
  <p style="margin:0;font-size:13px;color:#555">{{.Footer}}</p>
</body>
</html>`

// content is what changes per mail and per language.
type content struct {
	Lang     string
	Title    string
	Lead     string
	Action   string
	Fallback string
	Footer   string
	Link     string
}

var frame = template.Must(template.New("frame").Parse(bodyFrame))

// mails is keyed by kind and then by language. A locale we do not have copy for falls
// back to English rather than sending nothing.
var mails = map[notify.Kind]map[string]func(link string) (string, content){
	notify.KindEmailVerification: {
		"vi": func(link string) (string, content) {
			return "Xác nhận địa chỉ email của bạn", content{
				Lang:     "vi",
				Title:    "Xác nhận email",
				Lead:     "Nhấn vào nút bên dưới để xác nhận địa chỉ email này cho tài khoản ShopNexus của bạn. Liên kết có hiệu lực trong 24 giờ.",
				Action:   "Xác nhận email",
				Fallback: "Nếu nút không hoạt động, hãy mở liên kết này:",
				Footer:   "Nếu bạn không tạo tài khoản nào, hãy bỏ qua email này.",
				Link:     link,
			}
		},
		"en": func(link string) (string, content) {
			return "Confirm your email address", content{
				Lang:     "en",
				Title:    "Confirm your email",
				Lead:     "Use the button below to confirm this address for your ShopNexus account. The link is valid for 24 hours.",
				Action:   "Confirm email",
				Fallback: "If the button does not work, open this link:",
				Footer:   "If you did not create an account, you can ignore this email.",
				Link:     link,
			}
		},
	},
	notify.KindPasswordReset: {
		"vi": func(link string) (string, content) {
			return "Đặt lại mật khẩu ShopNexus", content{
				Lang:     "vi",
				Title:    "Đặt lại mật khẩu",
				Lead:     "Nhấn vào nút bên dưới để chọn mật khẩu mới. Liên kết có hiệu lực trong 1 giờ và chỉ dùng được một lần.",
				Action:   "Đặt lại mật khẩu",
				Fallback: "Nếu nút không hoạt động, hãy mở liên kết này:",
				// The line that matters: a reset nobody asked for is the signal of an
				// attempted takeover, and the mail has to say so.
				Footer: "Nếu bạn không yêu cầu đặt lại mật khẩu, hãy bỏ qua email này — mật khẩu hiện tại của bạn vẫn giữ nguyên.",
				Link:   link,
			}
		},
		"en": func(link string) (string, content) {
			return "Reset your ShopNexus password", content{
				Lang:     "en",
				Title:    "Reset your password",
				Lead:     "Use the button below to choose a new password. The link is valid for one hour and can be used once.",
				Action:   "Reset password",
				Fallback: "If the button does not work, open this link:",
				Footer:   "If you did not ask for a reset, ignore this email — your current password still works.",
				Link:     link,
			}
		},
	},
}

// language picks the copy for a BCP 47 locale: the base language decides, and anything
// unknown falls back to English.
func language(locale string) string {
	if len(locale) >= 2 && locale[:2] == "vi" {
		return "vi"
	}
	return "en"
}
