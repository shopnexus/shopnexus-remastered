package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"shopnexus-remastered/internal/client/pubsub"
)

type Order struct {
	ID       string `json:"id"`
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

func main() {
	// Example usage of the Kafka client
	cfg := pubsub.KafkaConfig{
		Config: pubsub.Config{
			Brokers: []string{"localhost:9092"},
			Timeout: 10 * time.Second,
			Decoder: json.Unmarshal,
			Encoder: json.Marshal,
		},
		Group: "test-group",
	}
	ctx := context.Background()

	client, err := pubsub.NewKafkaClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create Kafka client: %v", err)
	}
	defer client.Close()

	go func() {
		for {
			time.Sleep(5 * time.Second)
			if err = client.Publish(ctx, "orders", Order{
				ID:       "order" + time.Now().Format("20060102150405"),
				Item:     "Laptop",
				Quantity: 1,
			}); err != nil {
				log.Fatalf("Failed to publish message: %v", err)
			}
			if err = client.Publish(ctx, "orders", Order{
				ID:       "order" + time.Now().Format("20060102150405"),
				Item:     "Laptop",
				Quantity: 1,
			}); err != nil {
				log.Fatalf("Failed to publish message: %v", err)
			}
			fmt.Println("Published order")
		}
	}()

	if err = client.Subscribe(ctx, "orders", func(msg *pubsub.MessageDecoder) error {
		var order Order
		if err := msg.Decode(&order); err != nil {
			return err
		}
		log.Printf("Received order 1: %+v", order)
		return nil
	}); err != nil {
		log.Fatalf("Failed to subscribe to topic: %v", err)
	}

	if err = client.Subscribe(ctx, "orders", func(msg *pubsub.MessageDecoder) error {
		var order Order
		if err := msg.Decode(&order); err != nil {
			return err
		}
		log.Printf("Received order 2: %+v", order)
		return nil
	}); err != nil {
		log.Fatalf("Failed to subscribe to topic: %v", err)
	}

	log.Println("Subscribed to topic 'orders' successfully")

	//go func() {
	//	for {
	//		time.Sleep(10 * time.Second)
	//		log.Println("Kafka client is running and listening for messages...")
	//	}
	//}()

	select {} // Keep the main function running to listen for messages
}
