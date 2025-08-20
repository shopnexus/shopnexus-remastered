package common

import (
	"encoding/json"
	"fmt"
)

func UnmarshalURL(marshalledURL string) (string, error) {
	var result string
	err := json.Unmarshal([]byte(fmt.Sprintf(`"%s"`, marshalledURL)), &result)
	return result, err
}
