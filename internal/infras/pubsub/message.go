package pubsub

type MessageDecoder struct {
	Raw     []byte
	decoder func(data []byte, v any) error
}

func NewMessageDecoder(raw []byte, decoder func(data []byte, v any) error) *MessageDecoder {
	return &MessageDecoder{
		Raw:     raw,
		decoder: decoder,
	}
}

// Decode into any struct, like json.Decoder.
func (d *MessageDecoder) Decode(v any) error {
	return d.decoder(d.Raw, v)
}
