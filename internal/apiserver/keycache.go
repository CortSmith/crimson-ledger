package apiserver

import (
	"context"
	"sync"
	"time"

	"crimsonledger/internal/store"
)

const (
	// maxCachedKeys bounds the key cache (PLAN.md T05).
	maxCachedKeys = 1000
	// touchEvery throttles last_used_at writes to one per key per 10 minutes.
	touchEvery = 10 * time.Minute
)

type cachedKey struct {
	rec     store.KeyRecord
	expires time.Time
}

// keyCache serves key records from memory for ttl so polling does not hit the database on every
// request. Misses (unknown ids) are never cached. It also throttles Touch.
type keyCache struct {
	src store.KeyStore
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	recs    map[string]cachedKey
	touched map[string]time.Time
}

func newKeyCache(src store.KeyStore, ttl time.Duration, now func() time.Time) *keyCache {
	return &keyCache{src: src, ttl: ttl, now: now, recs: map[string]cachedKey{}, touched: map[string]time.Time{}}
}

func (c *keyCache) get(ctx context.Context, id string) (store.KeyRecord, error) {
	now := c.now()
	if c.ttl > 0 {
		c.mu.Lock()
		hit, ok := c.recs[id]
		c.mu.Unlock()
		if ok && now.Before(hit.expires) {
			return hit.rec, nil
		}
	}
	rec, err := c.src.Get(ctx, id)
	if err != nil || c.ttl <= 0 {
		return rec, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.recs) >= maxCachedKeys {
		c.evict(now)
	}
	c.recs[id] = cachedKey{rec: rec, expires: now.Add(c.ttl)}
	return rec, nil
}

func (c *keyCache) evict(now time.Time) {
	for id, e := range c.recs {
		if !now.Before(e.expires) {
			delete(c.recs, id)
		}
	}
	for id := range c.recs {
		if len(c.recs) < maxCachedKeys {
			return
		}
		delete(c.recs, id)
	}
}

// touch records last use, at most once per touchEvery per key. Failures are returned but never fatal.
func (c *keyCache) touch(ctx context.Context, id string) error {
	now := c.now()
	c.mu.Lock()
	if last, ok := c.touched[id]; ok && now.Sub(last) < touchEvery {
		c.mu.Unlock()
		return nil
	}
	if len(c.touched) >= maxCachedKeys {
		clear(c.touched)
	}
	c.touched[id] = now
	c.mu.Unlock()
	return c.src.Touch(ctx, id, now)
}
