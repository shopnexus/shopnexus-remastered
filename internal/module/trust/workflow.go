package trust

import (
	"context"
	"log/slog"

	trustapi "shopnexus/internal/module/trust/api"
)

// Reveal is the timed side of this module: the blind window closing. Unlike order, there is
// no per-entity durable run here — nothing is waiting on money, the only clock is one
// interval shared by every unrevealed rating, and the query that finds them reads a partial
// index. A workflow per feedback row would buy exactly what this loop already gives.
//
// Whatever drives order's sweep drives this one, and for the same reason: the pass is
// idempotent, so calling it again is a no-op rather than a second effect.
type Reveal struct{ svc trustapi.Service }

func NewReveal(svc trustapi.Service) *Reveal { return &Reveal{svc: svc} }

// sweepBatch is how many ratings one pass reveals. Bounded so a backlog is worked over
// several passes rather than in one transaction nobody can see the end of.
const sweepBatch = 100

// Sweep publishes every rating whose window has run out.
func (r *Reveal) Sweep(ctx context.Context, log *slog.Logger) {
	if revealed, err := r.svc.RevealDueFeedback(ctx, sweepBatch); err != nil {
		log.Error("reveal due feedback", "err", err)
	} else if revealed > 0 {
		log.Info("revealed feedback", "count", revealed)
	}
}
