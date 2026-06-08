package rankedset

import (
	"context"
	"fmt"
	"reflect"

	"github.com/guregu/null/v6"
)

// Client stores members scored for ranked retrieval (like heap trees or sorted sets).
type Client interface {
	Add(ctx context.Context, key string, value any, score float64) error
	TopByScore(ctx context.Context, key string, dest any, opts RangeOptions) error
	Delete(ctx context.Context, key string) error

	Ping() error
	Close() error
}

// RangeOptions defines optional bounds for ranked-set range queries.
type RangeOptions struct {
	Start  null.Float
	Stop   null.Float
	Offset null.Int
	Limit  null.Int // Negative limit means no limit (from redis docs)
}

// Config provides custom encoding and decoding functions for member values.
type Config struct {
	Decoder func(data []byte, v any) error
	Encoder func(value any) ([]byte, error)
}

// decodeMembers decodes each member into a new slice element via the configured
// decoder, then sets dest (a *[]T). Codec-agnostic — decode runs per element,
// so it makes no assumption about the wire framing.
func decodeMembers(decoder func([]byte, any) error, members []string, dest any) error {
	elemType := reflect.TypeOf(dest).Elem().Elem()
	slice := reflect.MakeSlice(reflect.TypeOf(dest).Elem(), len(members), len(members))

	for i, member := range members {
		elem := reflect.New(elemType)
		if err := decoder([]byte(member), elem.Interface()); err != nil {
			return fmt.Errorf("failed to decode member %d: %w", i, err)
		}
		slice.Index(i).Set(elem.Elem())
	}

	reflect.ValueOf(dest).Elem().Set(slice)
	return nil
}
