package pubsub

import (
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/nats-io/nats.go"
)

// JSONMarshaler marshals Watermill messages to/from NATS using JSON encoding.
// It uses the provided Encoder/Decoder functions (typically sonic.Marshal/Unmarshal).
type JSONMarshaler struct {
	Encoder func(any) ([]byte, error)
	Decoder func([]byte, any) error
}

type jsonEnvelope struct {
	UUID     string            `json:"uuid"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Payload  []byte            `json:"payload"`
}

// Marshal converts a Watermill message into a NATS message.
func (m *JSONMarshaler) Marshal(topic string, msg *message.Message) (*nats.Msg, error) {
	env := jsonEnvelope{
		UUID:     msg.UUID,
		Metadata: msg.Metadata,
		Payload:  msg.Payload,
	}

	data, err := m.Encoder(env)
	if err != nil {
		return nil, err
	}

	return &nats.Msg{
		Subject: topic,
		Data:    data,
	}, nil
}

// Unmarshal converts a NATS message back into a Watermill message.
func (m *JSONMarshaler) Unmarshal(natsMsg *nats.Msg) (*message.Message, error) {
	var env jsonEnvelope
	if err := m.Decoder(natsMsg.Data, &env); err != nil {
		return nil, err
	}

	msg := message.NewMessage(env.UUID, env.Payload)
	msg.Metadata = env.Metadata

	return msg, nil
}
