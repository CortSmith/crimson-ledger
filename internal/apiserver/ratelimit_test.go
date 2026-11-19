package apiserver

import (
	"context"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func TestMemoryLimiter_TokenBucket(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1731800000, 0)}
	l := NewMemoryLimiter(clock.Now)
	ctx := context.Background()
	allowed, denied := 0, 0
	var retry time.Duration
	for range 25 { // 120/min gives a burst of 20
		ok, wait, err := l.Allow(ctx, "k", 120, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			allowed++
		} else {
			denied++
			retry = wait
		}
	}
	if allowed != 20 || denied != 5 {
		t.Fatalf("allowed %d denied %d, want 20 and 5", allowed, denied)
	}
	if retry < 400*time.Millisecond || retry > 600*time.Millisecond {
		t.Errorf("retryAfter = %v, want about 500ms", retry)
	}
	clock.Advance(time.Second)
	for i := range 2 {
		if ok, _, _ := l.Allow(ctx, "k", 120, time.Minute); !ok {
			t.Errorf("call %d after 1 s should pass", i)
		}
	}
	if ok, _, _ := l.Allow(ctx, "k", 120, time.Minute); ok {
		t.Error("third call after 1 s should be denied")
	}
}

func TestMemoryLimiter_KeysAreIndependentAndBounded(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1731800000, 0)}
	l := NewMemoryLimiter(clock.Now)
	for i := range maxBuckets + 500 {
		if ok, _, _ := l.Allow(context.Background(), "ip:"+string(rune('a'+i%26))+time.Duration(i).String(), 60, time.Minute); !ok {
			t.Fatalf("a first request for a new key must pass (i=%d)", i)
		}
	}
	if n := l.size(); n > maxBuckets {
		t.Errorf("bucket map grew to %d, cap is %d", n, maxBuckets)
	}
}

func TestFailedAuthTracker_LocksAfterTenFailuresInWindow(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1731800000, 0)}
	tr := newFailedAuthTracker(clock.Now)
	for range failedAuthMax - 1 {
		tr.record("1.1.1.1")
	}
	if locked, _ := tr.locked("1.1.1.1"); locked {
		t.Fatal("9 failures must not lock")
	}
	tr.record("1.1.1.1")
	locked, wait := tr.locked("1.1.1.1")
	if !locked || wait <= 0 || wait > failedAuthLockout {
		t.Fatalf("locked=%v wait=%v after 10 failures", locked, wait)
	}
	if other, _ := tr.locked("2.2.2.2"); other {
		t.Error("another IP must be unaffected")
	}
	clock.Advance(failedAuthLockout + time.Second)
	if locked, _ := tr.locked("1.1.1.1"); locked {
		t.Error("the lock must expire")
	}
}

func TestFailedAuthTracker_OldFailuresFallOutOfWindow(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1731800000, 0)}
	tr := newFailedAuthTracker(clock.Now)
	for range failedAuthMax - 1 {
		tr.record("1.1.1.1")
	}
	clock.Advance(failedAuthWindow + time.Second)
	tr.record("1.1.1.1")
	if locked, _ := tr.locked("1.1.1.1"); locked {
		t.Error("failures older than the window must not count")
	}
}
