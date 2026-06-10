package besteffort

import (
	"net"
	"net/http"
)

// newTransport tunes the connection pool for BestEffort calls. Protocols enables
// h2 over TLS, prior-knowledge h2c for plain hosts, and HTTP/1.1 fallback.
func newTransport() *http.Transport {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: defaultDialKeepAlive,
		}).DialContext,
		Protocols:             &protocols,
		MaxIdleConns:          defaultMaxIdleConns,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:       defaultMaxConnsPerHost,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
	}
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: newTransport(),
	}
}
