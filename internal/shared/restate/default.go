package restatec

import "time"

// Defaults for the Restate ingress HTTP client.
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
