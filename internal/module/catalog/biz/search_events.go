package catalogbiz

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"

	"github.com/google/uuid"

	analyticmodel "shopnexus-server/internal/module/analytic/model"
	catalogmodel "shopnexus-server/internal/module/catalog/model"
	catalogutil "shopnexus-server/internal/module/catalog/util"
)

// AddInteractions processes a batch of analytic interaction events. Batching
// happens upstream in the bus subscription (see catalog/workers).
func (b *CatalogHandler) AddInteractions(ctx context.Context, events []analyticmodel.Interaction) error {
	if err := b.processEvents(ctx, events); err != nil {
		return fmt.Errorf("process events: %w", err)
	}

	// Interests changed → invalidate each affected account's recommend pool.
	seen := make(map[uuid.UUID]struct{})
	for _, ev := range events {
		if !ev.AccountID.Valid {
			continue
		}
		if _, ok := seen[ev.AccountID.UUID]; ok {
			continue
		}
		seen[ev.AccountID.UUID] = struct{}{}
		if err := b.cache.Delete(
			ctx,
			fmt.Sprintf(catalogmodel.CacheKeyRecommendPool, ev.AccountID.UUID.String()),
		); err != nil {
			b.logger.Error(
				"invalidate recommend pool",
				slog.String("account_id", ev.AccountID.UUID.String()),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// processEvents updates account interest vectors in the account_interest table
// based on analytic interaction events.
func (b *CatalogHandler) processEvents(ctx context.Context, events []analyticmodel.Interaction) error {
	if len(events) == 0 {
		return nil
	}

	// 1. Collect unique product IDs
	itemIDSet := make(map[uuid.UUID]struct{})
	for _, e := range events {
		if e.RefID != uuid.Nil {
			itemIDSet[e.RefID] = struct{}{}
		}
	}
	itemIDs := make([]uuid.UUID, 0, len(itemIDSet))
	for id := range itemIDSet {
		itemIDs = append(itemIDs, id)
	}

	// 2. Fetch product content vectors
	itemVectors, err := b.getProductVectors(ctx, itemIDs)
	if err != nil {
		return fmt.Errorf("get product vectors: %w", err)
	}

	// 3. Group events by account
	accountEvents := make(map[uuid.UUID][]analyticmodel.Interaction)
	for _, e := range events {
		if !e.AccountID.Valid {
			continue
		}
		accountEvents[e.AccountID.UUID] = append(accountEvents[e.AccountID.UUID], e)
	}

	// 4. Fetch existing account interests
	accountIDs := make([]uuid.UUID, 0, len(accountEvents))
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
		if err := b.upsertAccountInterests(ctx, accountID, interests, strengths); err != nil {
			return fmt.Errorf("upsert account %s: %w", accountID, err)
		}
	}

	return nil
}

type accountInterests struct {
	interests [][]float32
	strengths []float32
}

func aggregateProductWeights(events []analyticmodel.Interaction) map[uuid.UUID]float32 {
	weights := make(map[uuid.UUID]float32)
	for _, e := range events {
		if e.RefID == uuid.Nil {
			continue
		}
		weights[e.RefID] += catalogutil.GetEventWeight(strings.ToLower(string(e.EventType)))
	}
	return weights
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
