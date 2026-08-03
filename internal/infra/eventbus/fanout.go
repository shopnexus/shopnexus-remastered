package eventbus

import (
	"fmt"

	"github.com/nats-io/nats.go"
)

// fanoutSubjectPrefix keeps ephemeral fan-out out of the JetStream stream's subject
// space (bus.>), so a broadcast is never captured by a durable consumer.
const fanoutSubjectPrefix = "fanout."

// Broadcast publishes to every current subscriber of subject and to nobody else.
//
// Core NATS, deliberately not JetStream. JetStream's Subscribe creates a durable pull
// consumer, so replicas sharing one would split the messages between them instead of
// each receiving all — and a durable per replica leaves a consumer behind for every
// process that ever ran, orphaned outright by a kill -9. Nothing here is persisted:
// a message with no listener is dropped, which is the right semantics for a live
// socket, because a client that was disconnected re-reads over REST rather than
// replaying a day of history into a fresh connection.
func (n *NATS) Broadcast(subject string, payload []byte) error {
	if err := n.conn.Publish(fanoutSubjectPrefix+subject, payload); err != nil {
		return fmt.Errorf("eventbus: broadcast %s: %w", subject, err)
	}
	return nil
}

// OnBroadcast delivers every message on subject to h until cancel is called.
//
// h runs on the connection's dispatch goroutine, so it must not block: a handler that
// waits stalls delivery for every subject this connection carries, not just this one.
func (n *NATS) OnBroadcast(subject string, h func([]byte)) (func(), error) {
	sub, err := n.conn.Subscribe(fanoutSubjectPrefix+subject, func(msg *nats.Msg) {
		h(msg.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("eventbus: subscribe broadcast %s: %w", subject, err)
	}
	return func() {
		// The caller is going away regardless, so a failed unsubscribe is worth a log
		// at most — and the connection closing will drop it anyway.
		if err := sub.Unsubscribe(); err != nil {
			n.logger.Warn("eventbus: unsubscribe broadcast failed", "subject", subject, "err", err)
		}
	}, nil
}

// Compile-time assertion that *NATS implements the realtime.Fanout interface.
// This check will fail if the method signatures drift.
var _ interface {
	Broadcast(subject string, payload []byte) error
	OnBroadcast(subject string, h func([]byte)) (cancel func(), err error)
} = (*NATS)(nil)
