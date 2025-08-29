package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// SystemSeedData holds seeded system data for other seeders to reference
type SystemSeedData struct {
	Events      []db.SystemEvent
	SearchSyncs []db.SystemSearchSync
}

// SeedSystemSchema seeds the system schema with fake data
func SeedSystemSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData) (*SystemSeedData, error) {
	fmt.Println("⚙️ Seeding system schema...")

	data := &SystemSeedData{
		Events:      make([]db.SystemEvent, 0),
		SearchSyncs: make([]db.SystemSearchSync, 0),
	}

	// Create search sync records for different search engines
	searchEngines := []string{"Elasticsearch", "Algolia", "Meilisearch", "Solr", "Typesense"}
	for _, engine := range searchEngines {
		// Create some with recent sync times, some with older sync times
		var lastSynced time.Time
		if fake.Boolean().Bool() {
			// Recent sync (within last 24 hours)
			lastSynced = time.Now().Add(-time.Duration(fake.RandomDigit()%24) * time.Hour)
		} else {
			// Older sync (1-30 days ago)
			lastSynced = time.Now().AddDate(0, 0, -(fake.RandomDigit()%30 + 1))
		}

		searchSync, err := storage.CreateSearchSync(ctx, db.CreateSearchSyncParams{
			Name:       engine,
			LastSynced: pgtype.Timestamptz{Time: lastSynced, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create search sync for %s: %w", engine, err)
		}
		data.SearchSyncs = append(data.SearchSyncs, searchSync)
	}

	// Create system events
	eventTypes := db.AllSystemEventTypeValues()
	aggregateTypes := []string{
		"Account", "Customer", "Vendor", "Product", "Order", "Payment",
		"Refund", "Promotion", "Comment", "Brand", "Category", "Stock",
	}

	eventCount := cfg.AccountCount + cfg.ProductCount + cfg.OrderCount // Generate events for major entities
	for i := 0; i < eventCount; i++ {
		eventType := eventTypes[fake.RandomDigit()%len(eventTypes)]
		aggregateType := aggregateTypes[fake.RandomDigit()%len(aggregateTypes)]
		aggregateID := int64(fake.RandomDigit()%1000 + 1) // Random aggregate ID

		// Some events have account_id (user actions), some don't (system actions)
		var accountID *int64
		if fake.RandomDigit()%3 != 0 && len(accountData.Accounts) > 0 { // 66% chance of having account
			account := accountData.Accounts[fake.RandomDigit()%len(accountData.Accounts)]
			accountID = &account.ID
		}

		// Generate realistic event payload based on event type and aggregate
		payload := generateEventPayload(fake, eventType, aggregateType, aggregateID)
		payloadMarshal, _ := json.Marshal(payload)
		version := int64(fake.RandomDigit()%10 + 1) // Version 1-10

		// Event time within the last 30 days
		eventTime := time.Now().Add(-time.Duration(fake.RandomDigit()%30*24) * time.Hour)

		event, err := storage.CreateEvent(ctx, db.CreateEventParams{
			AccountID:     pgtype.Int8{Int64: ptr.DerefDefault(accountID, 0), Valid: accountID != nil},
			AggregateID:   aggregateID,
			AggregateType: aggregateType,
			EventType:     eventType,
			Payload:       payloadMarshal,
			Version:       version,
			DateCreated:   pgtype.Timestamptz{Time: eventTime, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create event %d: %w", i+1, err)
		}
		data.Events = append(data.Events, event)
	}

	fmt.Printf("✅ System schema seeded: %d events, %d search syncs\n",
		len(data.Events), len(data.SearchSyncs))

	return data, nil
}

// generateEventPayload creates realistic event payloads based on event type and aggregate
func generateEventPayload(fake *faker.Faker, eventType db.SystemEventType, aggregateType string, aggregateID int64) map[string]interface{} {
	payload := map[string]interface{}{
		"aggregate_id":   aggregateID,
		"aggregate_type": aggregateType,
		"event_type":     eventType,
		"timestamp":      time.Now().Unix(),
	}

	switch eventType {
	case "Created":
		payload["action"] = "create"
		switch aggregateType {
		case "Account":
			payload["data"] = map[string]interface{}{
				"email":    fake.Internet().Email(),
				"username": fake.Internet().User(),
				"type":     []string{"Customer", "Vendor"}[fake.RandomDigit()%2],
			}
		case "Product":
			payload["data"] = map[string]interface{}{
				"name":     fake.Pokemon().English(),
				"price":    fake.RandomFloat(2, 10, 1000),
				"category": fake.Lorem().Word(),
				"brand":    fake.Company().Name(),
			}
		case "Order":
			payload["data"] = map[string]interface{}{
				"customer_id":    fake.RandomDigit()%100 + 1,
				"total_amount":   fake.RandomFloat(2, 20, 500),
				"payment_method": []string{"COD", "Card", "EWallet"}[fake.RandomDigit()%3],
				"items_count":    fake.RandomDigit()%5 + 1,
			}
		case "Payment":
			payload["data"] = map[string]interface{}{
				"order_id": fake.RandomDigit()%100 + 1,
				"amount":   fake.RandomFloat(2, 20, 500),
				"method":   []string{"Card", "EWallet", "Crypto"}[fake.RandomDigit()%3],
				"status":   "Success",
			}
		}

	case "Updated":
		payload["action"] = "update"
		payload["changes"] = map[string]interface{}{
			"fields_updated": []string{"status", "updated_at"},
			"old_values":     map[string]interface{}{"status": "pending"},
			"new_values":     map[string]interface{}{"status": "active"},
		}

	case "Deleted":
		payload["action"] = "delete"
		payload["reason"] = []string{
			"User request", "Admin action", "Policy violation",
			"Expired", "Duplicate", "Data cleanup",
		}[fake.RandomDigit()%6]
	}

	// Add common metadata
	payload["metadata"] = map[string]interface{}{
		"source":      "system",
		"environment": "production",
		"version":     "1.0.0",
		"request_id":  fake.UUID().V4(),
	}

	return payload
}
