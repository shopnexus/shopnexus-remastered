package restatec

import (
	"net"
	"net/http"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
)

// newIngressClient builds an SDK ingress client backed by the tuned HTTP client.
func newIngressClient(baseURL string) *ingress.Client {
	return ingress.NewClient(baseURL, restate.WithHttpClient(newHTTPClient()))
}

// newRestateTransport tunes the connection pool for the Restate ingress.
func newRestateTransport() *http.Transport {
	return &http.Transport{
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
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: newRestateTransport(),
	}
}
