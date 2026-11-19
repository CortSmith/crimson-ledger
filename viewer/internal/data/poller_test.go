package data

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
)

type fakeSweeper struct {
	mu    sync.Mutex
	steps []func() (apiclient.SweepResult, error)
	calls int
}

func (f *fakeSweeper) Sweep(context.Context, apiclient.SweepParams) (apiclient.SweepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := min(f.calls, len(f.steps)-1)
	f.calls++
	return f.steps[i]()
}

func ok(nm bool) func() (apiclient.SweepResult, error) {
	return func() (apiclient.SweepResult, error) { return apiclient.SweepResult{NotModified: nm}, nil }
}
func fail(err error) func() (apiclient.SweepResult, error) {
	return func() (apiclient.SweepResult, error) { return apiclient.SweepResult{}, err }
}

// run drives a poller with a fake sleeper that records waits and cancels after n sleeps.
func run(t *testing.T, steps []func() (apiclient.SweepResult, error), sleeps int) (*Poller, []Update, []time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ups []Update
	var waits []time.Duration
	p := New(Options{
		Client: &fakeSweeper{steps: steps}, Params: func() apiclient.SweepParams { return apiclient.SweepParams{} },
		Interval: 10 * time.Second, Publish: func(u Update) { ups = append(ups, u) }, Jitter: func() float64 { return 0.5 },
		Sleep: func(c context.Context, d time.Duration) error {
			waits = append(waits, d)
			if len(waits) >= sleeps {
				cancel()
			}
			return c.Err()
		},
	})
	p.Run(ctx)
	return p, ups, waits
}

func TestPoller_PublishesFreshSweepButNotA304(t *testing.T) {
	_, ups, _ := run(t, []func() (apiclient.SweepResult, error){ok(false), ok(true), ok(true)}, 3)
	if len(ups) != 1 || ups[0].Result == nil {
		t.Fatalf("want exactly one sweep update, got %+v", ups)
	}
}

func TestPoller_BacksOffDoublingUpToCap(t *testing.T) {
	boom := errors.New("down")
	steps := []func() (apiclient.SweepResult, error){fail(boom)}
	_, _, waits := run(t, steps, 6)
	want := []time.Duration{20, 40, 80, 120, 120, 120}
	for i, w := range want {
		if waits[i] != w*time.Second {
			t.Fatalf("wait %d = %v, want %v (all %v)", i, waits[i], w*time.Second, waits)
		}
	}
}

func TestPoller_RecoveryResetsBackoff(t *testing.T) {
	_, _, waits := run(t, []func() (apiclient.SweepResult, error){fail(errors.New("x")), ok(false)}, 2)
	if waits[0] != 20*time.Second || waits[1] != 10*time.Second {
		t.Fatalf("waits %v", waits)
	}
}

func TestPoller_StopsOnAuthFailure(t *testing.T) {
	denied := &apiclient.APIError{Status: http.StatusUnauthorized, Code: "unauthorized"}
	p, ups, waits := run(t, []func() (apiclient.SweepResult, error){fail(denied)}, 5)
	if p.State() != StateAuthFailed || len(waits) != 0 || len(ups) != 1 {
		t.Fatalf("state %v waits %v ups %v", p.State(), waits, ups)
	}
}

func TestPoller_JitterStaysWithinTenPercent(t *testing.T) {
	for _, j := range []float64{0, 0.999999} {
		p := New(Options{Interval: 10 * time.Second, Jitter: func() float64 { return j }})
		d := p.next(0)
		if d < 9*time.Second || d > 11*time.Second {
			t.Fatalf("jitter %v gave %v", j, d)
		}
	}
}

func TestPoller_HiddenWindowDoesNotPollAndShowWakesIt(t *testing.T) {
	fs := &fakeSweeper{steps: []func() (apiclient.SweepResult, error){ok(false)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slept := make(chan struct{}, 8)
	p := New(Options{Client: fs, Params: func() apiclient.SweepParams { return apiclient.SweepParams{} }, Interval: time.Hour,
		Publish: func(Update) {}, Sleep: func(c context.Context, _ time.Duration) error { slept <- struct{}{}; <-c.Done(); return c.Err() }})
	p.SetVisible(false)
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	<-slept
	fs.mu.Lock()
	if fs.calls != 0 {
		t.Fatalf("hidden window polled %d times", fs.calls)
	}
	fs.mu.Unlock()
	p.SetVisible(true)
	<-slept // woke, swept, slept again
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.calls != 1 {
		t.Fatalf("calls after show = %d", fs.calls)
	}
	cancel()
	<-done
}

func TestNew_ClampsIntervalToMinimum(t *testing.T) {
	if p := New(Options{Interval: time.Second}); p.o.Interval != MinInterval {
		t.Fatalf("interval %v", p.o.Interval)
	}
}
