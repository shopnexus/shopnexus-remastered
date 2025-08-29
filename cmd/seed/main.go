package main

import "shopnexus-remastered/pkg/seed"

func main() {
	cfg := &seed.SeedConfig{
		AccountCount:      50,
		ProductCount:      200,
		OrderCount:        100,
		PromotionCount:    10,
		CommentCount:      150,
		ClearExistingData: true,
	}

	seed.MainWithCustomConfig(cfg)
}
