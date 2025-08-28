package main

import (
	"context"
	"encoding/json"
	"fmt"
	cache "shopnexus-remastered/internal/client/cache"
	"time"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	cache, _ := cache.NewRedisClient(cache.RedisConfig{
		Config: cache.Config{
			Decoder: json.Unmarshal,
			Encoder: json.Marshal,
		},
		Addr:     []string{"localhost:6379"},
		Password: "peaksehopnexuspassword",
		DB:       0,
	})

	var err error

	if err = cache.Set(context.Background(), "user:1", User{ID: 1, Name: "Alice"}, time.Hour); err != nil {
		panic(err)
	}

	var user User
	if err = cache.Get(context.Background(), "user:1", &user); err != nil {
		panic(err)
	}

	fmt.Println(user)

}
