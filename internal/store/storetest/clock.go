// Package storetest holds the contract suite every store.Store implementation must pass.
package storetest

import "time"

// FakeClock is a manually advanced clock so the suite controls time.
type FakeClock struct{ now time.Time }

// NewFakeClock starts at a fixed instant.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time { return c.now }

// Advance moves the clock forward.
func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
