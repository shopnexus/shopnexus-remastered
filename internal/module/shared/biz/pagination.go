package biz

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

func EncodeCursor(id int64, secret string) string {
	value := fmt.Sprintf("%d", id)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	signature := hex.EncodeToString(mac.Sum(nil))
	raw := fmt.Sprintf("%s.%s", value, signature)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func DecodeCursor(cursor, secret string) (int64, error) {
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}

	parts := strings.SplitN(string(raw), ".", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid cursor format")
	}

	value, signature := parts[0], parts[1]

	// Verify signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return 0, fmt.Errorf("invalid cursor signature")
	}

	var id int64
	if _, err = fmt.Sscanf(value, "%d", &id); err != nil {
		return 0, err
	}

	return id, nil
}
