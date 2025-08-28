package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

func encodeCursor(id int64, secret string) string {
	value := fmt.Sprintf("%d", id)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	signature := hex.EncodeToString(mac.Sum(nil))
	raw := fmt.Sprintf("%s.%s", value, signature)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor, secret string) (int64, error) {
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
	fmt.Sscanf(value, "%d", &id)
	return id, nil
}

func main() {
	secret := "my_secret_key"
	id := int64(12345)
	cursor := encodeCursor(id, secret)
	fmt.Println("Encoded Cursor:", cursor)

}
