package dynamostore

import (
	"math/rand/v2"
	"time"
)

// jitter returns a random duration in [0, d/2]; it spreads retries of concurrent writers (not a security use of randomness).
func jitter(d time.Duration) time.Duration {
	return time.Duration(rand.Int64N(int64(d/2) + 1)) //nolint:gosec // retry jitter only
}
