package apiserver

import (
	"errors"
	"fmt"
	"time"
)

// Defaults match api.json (PLAN.md section 4) and the security baseline (section 7).
const (
	// DefaultMaxBodyBytes is the request body limit: 256 KiB (PLAN.md section 5.1).
	DefaultMaxBodyBytes = 262144
	// DefaultRateLimitPerMin is the per-IP and per-key request budget (PLAN.md section 4).
	DefaultRateLimitPerMin = 120
	// DefaultKeyCacheTTL is how long a key record is served from memory; revocation takes up to this long.
	DefaultKeyCacheTTL = 30 * time.Second

	maxBodyBytesCeiling = 1 << 30
	maxRateLimitPerMin  = 1_000_000
)

// Options are the tunables of the API core.
type Options struct {
	MaxBodyBytes      int           // request body limit in bytes
	TrustProxyHeaders bool          // trust the last X-Forwarded-For entry as the client IP
	RateLimitPerMin   int           // requests per minute per client IP and per API key
	Version           string        // reported by /v1/health
	KeyCacheTTL       time.Duration // zero disables the key cache
}

// DefaultOptions returns the documented defaults.
func DefaultOptions() Options {
	return Options{
		MaxBodyBytes:    DefaultMaxBodyBytes,
		RateLimitPerMin: DefaultRateLimitPerMin,
		KeyCacheTTL:     DefaultKeyCacheTTL,
	}
}

// Validate reports the first out-of-range option.
func (o Options) Validate() error {
	switch {
	case o.MaxBodyBytes < 1 || o.MaxBodyBytes > maxBodyBytesCeiling:
		return fmt.Errorf("max body bytes %d out of range 1..%d", o.MaxBodyBytes, maxBodyBytesCeiling)
	case o.RateLimitPerMin < 1 || o.RateLimitPerMin > maxRateLimitPerMin:
		return fmt.Errorf("rate limit %d out of range 1..%d", o.RateLimitPerMin, maxRateLimitPerMin)
	case o.KeyCacheTTL < 0:
		return errors.New("key cache ttl must not be negative")
	}
	return nil
}
