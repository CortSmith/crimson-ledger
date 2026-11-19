package apiclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoff_Bounds(t *testing.T) {
	for attempt := 0; attempt < 4; attempt++ {
		base := 500 * time.Millisecond << attempt
		for _, j := range []float64{-0.3, 0, 0.3} {
			d := backoff(attempt, j)
			if d < time.Duration(float64(base)*0.7)-1 || d > time.Duration(float64(base)*1.3)+1 {
				t.Errorf("attempt %d jitter %v: %v", attempt, j, d)
			}
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for h, want := range map[string]time.Duration{"3": 3 * time.Second, "120": maxRetryAfter, "0": 0,
		now.Add(10 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"): 10 * time.Second,
		now.Add(-time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"):       0} {
		if got, ok := parseRetryAfter(h, now); !ok || got != want {
			t.Errorf("%q: %v %v want %v", h, got, ok, want)
		}
	}
	for _, h := range []string{"", "soon", "-5"} {
		if _, ok := parseRetryAfter(h, now); ok {
			t.Errorf("%q accepted", h)
		}
	}
}

func TestClassify(t *testing.T) {
	rnd := func() float64 { return 0.5 }
	now := time.Now()
	if d := classify(0, errors.New("eof"), 0, "", now, rnd); !d.retry {
		t.Error("network error not retried")
	}
	if d := classify(0, context.Canceled, 0, "", now, rnd); d.retry {
		t.Error("cancel retried")
	}
	if d := classify(503, nil, maxAttempts-1, "", now, rnd); d.retry {
		t.Error("retried past the limit")
	}
	if d := classify(429, nil, 0, "7", now, rnd); !d.retry || d.wait != 7*time.Second {
		t.Errorf("%+v", d)
	}
	for _, s := range []int{200, 400, 401, 403, 404, 413, 500} {
		if classify(s, nil, 0, "", now, rnd).retry {
			t.Errorf("%d retried", s)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	if IsRetryable(nil) || IsRetryable(ErrCrossHostRedirect) || !IsRetryable(errors.New("reset")) || !IsRetryable(newAPIError(503, "", "", "")) || IsRetryable(newAPIError(404, "", "", "")) {
		t.Error("IsRetryable wrong")
	}
}
