package besteffort

import (
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

// newTransport tunes the connection pool for BestEffort calls.
// ForceAttemptHTTP2 negotiates h2 over TLS; ConfigureTransports also wires the
// h2 framer for prior-knowledge h2c while keeping HTTP/1.1 fallback for plain hosts.
func newTransport() *http.Transport {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: defaultDialKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          defaultMaxIdleConns,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:       defaultMaxConnsPerHost,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
	}
	_, _ = http2.ConfigureTransports(t)
	return t
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: newTransport(),
	}
}
