package main

import (
	"encoding/json"
	"fmt"

	sharedmodel "shopnexus-remastered/internal/module/shared/model"
)

type MyInt int

func (m MyInt) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"custom:%d\"", m)), nil
}

func main() {
	var x sharedmodel.Concurrency = 4212312312123123
	data, _ := json.Marshal(x)
	fmt.Println(string(data)) // "custom:42"
}
