package testkit

import (
	"sync"
	"time"
)

// Epoch is the instant every FakeClock starts at and every builder defaults to.
var Epoch = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// FakeClock is a manually advanced clock. It is safe for concurrent use.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFakeClock starts a clock at Epoch.
func NewFakeClock() *FakeClock { return &FakeClock{now: Epoch} }

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward and fires every After whose deadline has passed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- c.now // buffered: never blocks
	}
	c.waiters = kept
}

// After returns a channel that receives the fake time once the clock has advanced by d.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, waiter{at: c.now.Add(d), ch: ch})
	return ch
}
