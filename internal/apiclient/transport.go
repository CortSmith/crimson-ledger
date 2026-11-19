package apiclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"crimsonledger/internal/config"
)

const maxRedirects = 2

// newBase validates the server URL like config.ServerURL and returns it without a trailing slash.
func newBase(raw string) (*url.URL, error) {
	if err := config.ValidateServerURL(raw); err != nil {
		return nil, errors.New("the server address must start with https:// (http:// only for localhost) and have no password")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("the server address is not a valid URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

// tlsConfig is TLS 1.2+ with the system roots plus an optional PEM file. There is no way to skip verification.
func tlsConfig(caFile string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) // #nosec G304 -- user-configured trust anchor
		if err != nil {
			return nil, fmt.Errorf("cannot read the CA file: %w", errors.Unwrap(err))
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("the CA file contains no usable certificate")
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, nil
}

// newHTTPClient builds the one http.Client. Redirects stay on the same scheme and host:port, at most maxRedirects.
// The Authorization header is never part of the client: it is set per request, so no redirect can carry it.
func newHTTPClient(base *url.URL, tc *tls.Config, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tc, Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, MaxIdleConns: 4, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return ErrTooManyRedirects
			}
			if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
				return ErrCrossHostRedirect
			}
			return nil
		},
	}
}
