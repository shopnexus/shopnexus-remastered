package sepay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

// addRef appends `?ref=<refID>` to a URL, preserving any existing query
// string. SePay redirects to these URLs verbatim, so the FE result page only
// needs the transaction ID — outcome and provider are derived server-side by
// looking up the transaction row.
func addRef(returnURL, refID string) string {
	sep := "?"
	if strings.Contains(returnURL, "?") {
		sep = "&"
	}
	return returnURL + sep + "ref=" + url.QueryEscape(refID)
}

// signFields builds the SePay signature string from ordered fields and signs with HMAC-SHA256.
// Format: "field1=value1,field2=value2,..." → HMAC-SHA256 → base64.
func signFields(fields []keyValue, secret string) string {
	var parts []string
	for _, kv := range fields {
		if kv.value != "" {
			parts = append(parts, kv.key+"="+kv.value)
		}
	}
	data := strings.Join(parts, ",")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type keyValue struct {
	key   string
	value string
}
