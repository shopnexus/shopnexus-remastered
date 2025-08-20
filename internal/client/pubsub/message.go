package pubsub

type MessageDecoder struct {
	raw     []byte
	decoder func(data []byte, v any) error
}

func NewMessageDecoder(raw []byte, decoder func(data []byte, v any) error) *MessageDecoder {
	return &MessageDecoder{
		raw:     raw,
		decoder: decoder,
	}
}

// Decode into any struct, like json.Decoder
func (d *MessageDecoder) Decode(v any) error {
	return d.decoder(d.raw, v)
}

// Raw returns the raw byte value
func (d *MessageDecoder) Raw() []byte {
	return d.raw
}
