package main

import (
	"context"
	"fmt"
	"log"

	"shopnexus-remastered/internal/client/search"

	"github.com/elastic/go-elasticsearch/v9"
)

type User struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func main() {
	client, _ := search.NewElasticsearchClient(elasticsearch.Config{
		Addresses: []string{"http://localhost:9200"},
	})

	ctx := context.Background()

	if err := client.IndexDocuments(ctx, "test", "khoa", User{
		Name: "Khoa",
		Age:  10,
	}); err != nil {
		log.Println(err)
	}

	results, _ := client.Search(ctx, "test", "kho joh", 10)
	fmt.Println(results)

}
