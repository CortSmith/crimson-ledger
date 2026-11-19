// Package data polls the API for live contacts and hands each change to the page.
package data

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"crimsonledger/internal/apiclient"
)

// Polling bounds (PLAN T14: default 15 s, minimum 5 s, backoff x2 up to 120 s).
const (
	MinInterval = 5 * time.Second
	MaxBackoff  = 120 * time.Second
	jitterRatio = 0.1 // +-10 % so several viewers do not hit the server in step
)

// State is the connection state shown by the status dot.
type State string

// Connection states.
const (
	StateConnecting State = "connecting"
	StateOK         State = "ok"
	StateError      State = "error"      // retrying with backoff
	StateAuthFailed State = "authFailed" // key rejected; polling has stopped
)

// Sweeper is the part of apiclient.Client the poller uses.
type Sweeper interface {
	Sweep(ctx context.Context, p apiclient.SweepParams) (apiclient.SweepResult, error)
}

// Update is what the poller publishes: a fresh sweep, or only a state change (Result nil).
type Update struct {
	State  State
	Result *apiclient.SweepResult
	Err    error
}

// Options configures a Poller. Sleep and Jitter are injected so tests are deterministic.
type Options struct {
	Client   Sweeper
	Params   func() apiclient.SweepParams
	Interval time.Duration
	Publish  func(Update)
	Sleep    func(ctx context.Context, d time.Duration) error
	Jitter   func() float64 // in [0,1)
}

// Poller runs one goroutine and one timer. It is safe to call SetVisible and Wake from any goroutine.
type Poller struct {
	o       Options
	visible atomic.Bool
	wake    chan struct{}
	mu      sync.Mutex
	state   State
}

// New returns a Poller that is visible and in StateConnecting.
func New(o Options) *Poller {
	if o.Interval < MinInterval {
		o.Interval = MinInterval
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.Jitter == nil {
		o.Jitter = rand.Float64
	}
	p := &Poller{o: o, wake: make(chan struct{}, 1), state: StateConnecting}
	p.visible.Store(true)
	return p
}

// SetVisible pauses polling while the window is hidden and sweeps at once when it comes back.
func (p *Poller) SetVisible(v bool) {
	if p.visible.Swap(v) != v && v {
		p.Wake()
	}
}

// Wake makes the next sweep happen now (settings changed, ruleset switched, window shown).
func (p *Poller) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// State returns the current connection state.
func (p *Poller) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Run polls until ctx ends or the key is rejected.
func (p *Poller) Run(ctx context.Context) {
	failures := 0
	for {
		wait := p.o.Interval
		if p.visible.Load() {
			res, err := p.o.Client.Sweep(ctx, p.o.Params())
			if ctx.Err() != nil {
				return
			}
			failures = p.handle(res, err, failures)
			if p.State() == StateAuthFailed {
				return
			}
			wait = p.next(failures)
		}
		if p.pause(ctx, wait) != nil {
			return
		}
	}
}

func (p *Poller) handle(res apiclient.SweepResult, err error, failures int) int {
	switch {
	case apiclient.IsAuth(err):
		p.set(StateAuthFailed, Update{State: StateAuthFailed, Err: err})
		return failures
	case err != nil:
		p.set(StateError, Update{State: StateError, Err: err})
		return failures + 1
	}
	changed := p.State() != StateOK
	p.mu.Lock()
	p.state = StateOK
	p.mu.Unlock()
	if !res.NotModified {
		p.o.Publish(Update{State: StateOK, Result: &res})
	} else if changed {
		p.o.Publish(Update{State: StateOK})
	}
	return 0
}

func (p *Poller) set(s State, u Update) {
	p.mu.Lock()
	changed := p.state != s
	p.state = s
	p.mu.Unlock()
	if changed || u.Err != nil {
		p.o.Publish(u)
	}
}

// next is the base interval, doubled per consecutive failure up to MaxBackoff, with jitter.
func (p *Poller) next(failures int) time.Duration {
	d := p.o.Interval
	for i := 0; i < failures && d < MaxBackoff; i++ {
		d *= 2
	}
	d = min(d, MaxBackoff)
	return time.Duration(float64(d) * (1 + jitterRatio*(2*p.o.Jitter()-1)))
}

// pause sleeps for d but returns early when woken.
func (p *Poller) pause(ctx context.Context, d time.Duration) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-p.wake:
			cancel()
		case <-sctx.Done():
		}
	}()
	err := p.o.Sleep(sctx, d)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_ = err // woken early: sweep now
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
