package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"crimsonledger/internal/config"
)

const healthTimeout = 2 * time.Second

// runHealthcheck is the container probe: distroless images have no curl, so the API binary checks itself.
func runHealthcheck(ctx context.Context, args []string, io IO) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := fs.String("url", "", "health URL (default: derived from api.json)")
	if code, ok := parseFlags(fs, args, io.Stderr); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return usageFail(io.Stderr, "healthcheck", "takes no arguments, only flags")
	}
	cfg, err := config.Load("api.json", config.APIDefaults)
	if err != nil {
		return fail(io.Stderr, "healthcheck", err)
	}
	target := *url
	if target == "" {
		if target, err = healthURL(cfg); err != nil {
			return fail(io.Stderr, "healthcheck", err)
		}
	}
	client, err := healthClient(cfg)
	if err != nil {
		return fail(io.Stderr, "healthcheck", err)
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fail(io.Stderr, "healthcheck", err)
	}
	resp, err := client.Do(req) // #nosec G704 -- the URL is the operator's own flag or api.json
	if err != nil {
		return fail(io.Stderr, "healthcheck", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fail(io.Stderr, "healthcheck", fmt.Errorf("%s answered %s", target, resp.Status))
	}
	return exitOK
}

// healthURL is where the local API answers: a wildcard bind is probed through loopback.
func healthURL(cfg config.APIConfig) (string, error) {
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("listen address %q must look like host:port: %w", cfg.Listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	scheme := "http"
	if cfg.TLSCert != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/v1/health", nil
}

// healthClient trusts the API's own certificate (never InsecureSkipVerify) when TLS is on.
func healthClient(cfg config.APIConfig) (*http.Client, error) {
	c := &http.Client{Timeout: healthTimeout}
	if cfg.TLSCert == "" {
		return c, nil
	}
	pem, err := os.ReadFile(cfg.TLSCert)
	if err != nil {
		return nil, fmt.Errorf("read tls_cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("tls_cert %s holds no certificate", cfg.TLSCert)
	}
	c.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return c, nil
}
