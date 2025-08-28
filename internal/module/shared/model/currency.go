package sharedmodel

import "strconv"

const FloatingPointPrecision = 1e9

type Concurrency int64

// Int64 returns the Concurrency value as an int64 (original, no scaling by FloatingPointPrecision).
func (c Concurrency) Int64() int64 {
	return int64(c)
}

func (c Concurrency) String() string {
	return strconv.FormatFloat(float64(c)/FloatingPointPrecision, 'f', -1, 64)
}

func (c Concurrency) MarshalJSON() ([]byte, error) {
	return []byte(c.String()), nil
}

// Float64 returns the Concurrency value as a float64 but scaled by FloatingPointPrecision.
func (c Concurrency) Float64() float64 {
	return float64(c) / FloatingPointPrecision
}

func FromInt64(v int64) Concurrency {
	return Concurrency(v * FloatingPointPrecision)
}

func FromFloat64(v float64) Concurrency {
	return Concurrency(v * FloatingPointPrecision)
}
