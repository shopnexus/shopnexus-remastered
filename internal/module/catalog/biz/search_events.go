package catalogbiz

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"

	restate "github.com/restatedev/sdk-go"

	"github.com/google/uuid"

	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	catalogutil "shopnexus-server/internal/module/catalog/util"
)

// AddInteraction buffers an analytic interaction event and flushes the batch when full.
func (b *CatalogHandler) AddInteraction(ctx restate.Context, params analyticmodel.Interaction) error {
	// Buffer the event under lock, release before processing
	// WIP: storing buffer in memory kinda sucks, but good enough for now
	b.mu.Lock()
	b.buffer = append(b.buffer, params)
	if len(b.buffer) < b.batchSize {
		b.mu.Unlock()
		return nil
	}
	toInsert := b.buffer
	b.buffer = make([]analyticmodel.Interaction, 0, b.batchSize)
	b.mu.Unlock()

	// Process events without holding the buffer lock (involves Milvus network calls)
	if err := b.ProcessEvents(ctx, toInsert); err != nil {
		return err
	}

	// Remove old recommendations for all affected accounts
	seen := make(map[uuid.UUID]struct{})
	for _, ev := range toInsert {
		if !ev.AccountID.Valid {
			continue
		}
		if _, ok := seen[ev.AccountID.UUID]; ok {
			continue
		}
		seen[ev.AccountID.UUID] = struct{}{}
		if err := b.cache.Delete(
			ctx,
			fmt.Sprintf(catalogmodel.CacheKeyRecommendProduct, ev.AccountID.UUID.String()),
		); err != nil {
			b.logger.Error(
				"failed to reset feed offset for account",
				slog.String("account_id", ev.AccountID.UUID.String()),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// ProcessEvents updates account interest vectors in Milvus based on analytic interaction events.
func (b *CatalogHandler) ProcessEvents(ctx restate.Context, events []analyticmodel.Interaction) error {
	if len(events) == 0 {
		return nil
	}

	// 1. Collect unique product IDs
	itemIDSet := make(map[string]struct{})
	for _, e := range events {
		if e.RefID != uuid.Nil {
			itemIDSet[e.RefID.String()] = struct{}{}
		}
	}
	itemIDs := make([]string, 0, len(itemIDSet))
	for id := range itemIDSet {
		itemIDs = append(itemIDs, id)
	}

	// 2. Fetch product content vectors from Milvus
	itemVectors, err := b.getProductVectors(ctx, itemIDs)
	if err != nil {
		return fmt.Errorf("get product vectors: %w", err)
	}

	// 3. Group events by account
	accountEvents := make(map[string][]analyticmodel.Interaction)
	for _, e := range events {
		if !e.AccountID.Valid {
			continue
		}
		aid := e.AccountID.UUID.String()
		accountEvents[aid] = append(accountEvents[aid], e)
	}

	// 4. Fetch existing account interests
	accountIDs := make([]string, 0, len(accountEvents))
	for id := range accountEvents {
		accountIDs = append(accountIDs, id)
	}
	existingAccounts, err := b.getAccountInterests(ctx, accountIDs)
	if err != nil {
		return fmt.Errorf("get account interests: %w", err)
	}

	// 5. Process each account's events
	for accountID, acctEvents := range accountEvents {
		interests, strengths := catalogutil.DefaultInterests(ContentVectorDim)
		if existing, ok := existingAccounts[accountID]; ok {
			interests = existing.interests
			strengths = existing.strengths
		}

		// Aggregate event weights per product
		productWeights := aggregateProductWeights(acctEvents)

		for productID, weight := range productWeights {
			productVec, ok := itemVectors[productID]
			if !ok {
				continue
			}
			if weight > 0 {
				catalogutil.AssignPositive(interests, strengths, productVec, weight)
			} else if weight < 0 {
				catalogutil.AssignNegative(interests, strengths, productVec, weight)
			}
		}

		// 6. Upsert updated account
		// account_number removed from analytic; Milvus "number" column now always 0 until collection is migrated.
		_ = acctEvents
		if err := b.upsertAccountInterests(
			ctx,
			accountID,
			0,
			interests,
			strengths,
		); err != nil {
			return fmt.Errorf("upsert account %s: %w", accountID, err)
		}
	}

	return nil
}

type accountInterests struct {
	interests [][]float32
	strengths []float32
}

func aggregateProductWeights(events []analyticmodel.Interaction) map[string]float32 {
	weights := make(map[string]float32)
	for _, e := range events {
		if e.RefID == uuid.Nil {
			continue
		}
		weights[e.RefID.String()] += catalogutil.GetEventWeight(strings.ToLower(string(e.EventType)))
	}
	return weights
}

func accountOutputFields() []string {
	fields := make([]string, 0, 1+catalogutil.NumInterests*2)
	fields = append(fields, "id")
	for i := 1; i <= catalogutil.NumInterests; i++ {
		fields = append(fields, fmt.Sprintf("interest_%d", i))
		fields = append(fields, fmt.Sprintf("strength_%d", i))
	}
	return fields
}

// InterleaveShuffle splits each input slice into numParts chunks,
// combines the chunks for each part, and shuffles within each part.
// This ensures every part contains a proportional mix of all input slices.
func InterleaveShuffle[T any](numParts int, groups ...[]T) []T {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if total == 0 || numParts <= 0 {
		return nil
	}
	if numParts > total {
		numParts = total
	}

	splitInto := func(items []T) [][]T {
		parts := make([][]T, numParts)
		partSize := len(items) / numParts
		remainder := len(items) % numParts
		idx := 0
		for i := range numParts {
			size := partSize
			if i < remainder {
				size++
			}
			parts[i] = items[idx : idx+size]
			idx += size
		}
		return parts
	}

	splits := make([][][]T, len(groups))
	for i, g := range groups {
		splits[i] = splitInto(g)
	}

	result := make([]T, 0, total)

	for i := range numParts {
		var part []T
		for _, s := range splits {
			part = append(part, s[i]...)
		}

		rand.Shuffle(len(part), func(a, b int) {
			part[a], part[b] = part[b], part[a]
		})

		result = append(result, part...)
	}

	return result
}
