package analyticbiz

import (
	"time"

	restate "github.com/restatedev/sdk-go"

	accountmodel "shopnexus-server/internal/module/account/model"
	analyticdb "shopnexus-server/internal/module/analytic/db/sqlc"
	analyticmodel "shopnexus-server/internal/module/analytic/model"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

type CreateInteraction struct {
	Account   accountmodel.AuthenticatedAccount
	EventType analyticmodel.Event
	RefType   analyticmodel.InteractionRefType
	RefID     string
}

type CreateInteractionParams struct {
	Interactions []CreateInteraction
}

// CreateInteraction records a batch of user interactions and fans out popularity events.
func (b *AnalyticHandler) CreateInteraction(ctx restate.Context, params CreateInteractionParams) error {
	args := lo.Map(
		params.Interactions,
		func(interaction CreateInteraction, _ int) analyticdb.CreateBatchInteractionParams {
			return analyticdb.CreateBatchInteractionParams{
				AccountID:   uuid.NullUUID{UUID: interaction.Account.ID, Valid: true},
				EventType:   string(interaction.EventType),
				RefType:     analyticdb.AnalyticInteractionRefType(interaction.RefType),
				RefID:       interaction.RefID,
				Metadata:    []byte("{}"),
				DateCreated: time.Now(),
			}
		},
	)

	b.storage.Querier().
		CreateBatchInteraction(ctx, args).
		QueryRow(func(_ int, ai analyticdb.AnalyticInteraction, err error) {
			if err == nil {
				refID, _ := uuid.Parse(ai.RefID)

				// Fan out to HandlePopularityEvent and CatalogBiz.AddInteraction via Restate
				event := analyticmodel.Interaction{
					ID:          ai.ID,
					AccountID:   ai.AccountID,
					EventType:   analyticmodel.Event(ai.EventType),
					RefType:     analyticmodel.InteractionRefType(ai.RefType),
					RefID:       refID,
					Metadata:    ai.Metadata,
					DateCreated: ai.DateCreated,
				}
				restate.ServiceSend(ctx, "Analytic", "HandlePopularityEvent").Send(event)
				restate.ServiceSend(ctx, "Catalog", "AddInteraction").Send(event)
			} else {
				b.logger.Error("create analytic interaction: %w", "error", err)
			}
		})

	return nil
}
