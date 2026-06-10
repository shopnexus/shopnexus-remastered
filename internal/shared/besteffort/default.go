package besteffort

import "time"

// Defaults for the BestEffort HTTP client connection pool.
const (
	defaultDialTimeout           = 5 * time.Second
	defaultDialKeepAlive         = 30 * time.Second
	defaultMaxIdleConns          = 200
	defaultMaxIdleConnsPerHost   = 100
	defaultMaxConnsPerHost       = 200
	defaultIdleConnTimeout       = 90 * time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
	defaultExpectContinueTimeout = 1 * time.Second
	defaultRequestTimeout        = 30 * time.Second
)
