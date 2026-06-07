package analyticmodel

import "shopnexus-server/internal/infras/bus"

// TopicInteractionCreated fans each recorded interaction out to subscribers
// (analytic popularity scoring, catalog search popularity, ...). Analytic
// publishes; consumers subscribe via their module workers, so the publisher
// never knows who listens.
var TopicInteractionCreated = bus.NewTopic[Interaction]("analytic.interaction.created")
