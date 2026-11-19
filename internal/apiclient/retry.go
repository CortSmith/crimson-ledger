package apiclient

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Retry policy constants (T11 behaviour rules).
const (
	maxAttempts   = 5
	baseBackoff   = 500 * time.Millisecond
	maxRetryAfter = 60 * time.Second
	jitterSpan    = 0.3
)

// retryableStatus is true for the statuses worth another try: 429, 502, 503, 504.
func retryableStatus(s int) bool {
	return s == http.StatusTooManyRequests || s == http.StatusBadGateway ||
		s == http.StatusServiceUnavailable || s == http.StatusGatewayTimeout
}

// backoff is 0.5 s doubled per attempt (attempt 0 -> 0.5 s), scaled by 1+jitter where jitter is in [-0.3, 0.3].
func backoff(attempt int, jitter float64) time.Duration {
	d := float64(baseBackoff) * math.Pow(2, float64(attempt))
	return time.Duration(d * (1 + jitter))
}

// parseRetryAfter reads whole seconds or an HTTP date; the result is capped at maxRetryAfter.
func parseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	if h == "" {
		return 0, false
	}
	var d time.Duration
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(now)
	} else {
		return 0, false
	}
	return min(max(d, 0), maxRetryAfter), true
}

// decision is what classify tells the retry loop.
type decision struct {
	retry bool
	wait  time.Duration
}

// classify decides whether an attempt (status, or err when no response arrived) is retried and after how long.
// attempt counts from 0. rnd returns a number in [0,1). It does no I/O.
func classify(status int, err error, attempt int, retryAfter string, now time.Time, rnd func() float64) decision {
	if attempt >= maxAttempts-1 {
		return decision{}
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || permanentNetwork(err) {
			return decision{}
		}
	} else if !retryableStatus(status) {
		return decision{}
	}
	if ra, ok := parseRetryAfter(retryAfter, now); ok && err == nil {
		return decision{true, ra}
	}
	return decision{true, backoff(attempt, (rnd()*2-1)*jitterSpan)}
}
