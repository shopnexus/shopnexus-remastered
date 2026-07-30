package observability

import (
	"context"
	"time"

	"shopnexus/internal/infra/eventbus"
)

// observedTopics are the bus topics mirrored into business_events. Add a new
// topic name here to capture it (kept as strings so observability need not
// import every module's event package).
//
// Adding one is a disclosure decision, not a config change. The payload is copied
// verbatim into a table every Grafana user can read, which is a wider audience than
// whoever may read the rows the event came from — so a topic only belongs here if its
// payload is ids, amounts, statuses and timestamps. An address, a name, a phone or an
// email in a published event puts that data on a dashboard, and nothing downstream of
// this list will catch it.
var observedTopics = []string{
	"order.placed",
}

// subscribeEvents registers an observability consumer group on each observed
// topic that copies raw payloads into the business_events hypertable.
func subscribeEvents(b eventbus.Client, s *Sink) {
	for _, topic := range observedTopics {
		topic := topic
		b.Subscribe(topic, "observability", func(_ context.Context, payloads [][]byte) error {
			for _, p := range payloads {
				s.RecordEvent(topic, p)
			}
			return nil
		}, eventbus.SubscribeOptions{BatchSize: 50, Linger: 2 * time.Second})
	}
}
