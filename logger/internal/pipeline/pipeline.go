// Package pipeline is the scout's engine: tail the combat log, parse, track, batch, upload.
//
// Stages are goroutines joined by bounded channels, so a slow server slows the tailer instead of growing
// memory. Every stage ends when the context is cancelled; Stop then flushes pending contacts once.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/clock"
	"crimsonledger/internal/logparse/parser"
	"crimsonledger/internal/logparse/tailer"
)

const (
	channelSize = 1024 // bounded: backpressure, never unbounded memory
	// finalFlushTimeout is how long Stop waits to upload what is pending (T13: 3 s).
	finalFlushTimeout = 3 * time.Second
	minBackoff        = time.Second
	maxBackoff        = time.Minute
)

// State is what the pipeline is doing.
type State string

// Pipeline states. The app adds Idle, Connecting and Paused around these.
const (
	StateWatching  State = "watching"  // running, no log file seen yet
	StateUploading State = "uploading" // reading the log and sending
	StateError     State = "error"     // cannot send; the reason says why
)

// ErrKeyRejected ends Run when the server refuses the API key. Retrying cannot help.
var ErrKeyRejected = errors.New("API key rejected")

// Sender is the part of apiclient.Client the pipeline needs.
type Sender interface {
	PostPing(ctx context.Context, p domain.Ping) (apiclient.PingResult, error)
}

// Source follows a log. *tailer.Follower implements it.
type Source interface {
	Run(ctx context.Context, handle func(line string)) error
}

// Stats are live counters, safe to read while running.
type Stats struct {
	Lines, Contacts, PingsSent, Dropped atomic.Int64
	LastSuccess                         atomic.Int64 // unix ms, 0 = never
}

// Options wire a Pipeline. Source, Sender, Queue and Rand are required.
type Options struct {
	Source   Source
	Sender   Sender
	Queue    *Queue
	Batch    BatcherOptions
	Interval time.Duration // flush period, default 2 s
	Clock    clock.Clock
	// MinBackoff is the first retry wait after a failed upload (default 1 s); it doubles up to one minute.
	MinBackoff time.Duration
	Rand       io.Reader
	// OnState is called on changes; reason is plain language. OnActivity gets short log-free messages (never keys or raw log lines).
	OnState    func(s State, reason string)
	OnActivity func(msg string)
}

// Pipeline runs the stages once.
type Pipeline struct {
	o     Options
	Stats Stats
	state atomic.Value // State
}

// New returns a Pipeline.
func New(o Options) *Pipeline {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = minBackoff
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	o.Batch.Rand = o.Rand
	return &Pipeline{o: o}
}

func (p *Pipeline) setState(s State, reason string) {
	if old, _ := p.state.Load().(State); old == s && reason == "" {
		return
	}
	p.state.Store(s)
	if p.o.OnState != nil {
		p.o.OnState(s, reason)
	}
}

func (p *Pipeline) activity(msg string) {
	if p.o.OnActivity != nil {
		p.o.OnActivity(msg)
	}
}

// Run blocks until ctx is cancelled (returns nil after the final flush) or the key is rejected
// (returns ErrKeyRejected).
func (p *Pipeline) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan string, channelSize)
	events := make(chan parser.Event, channelSize)
	pings := make(chan domain.Ping, channelSize)
	var wg sync.WaitGroup
	stage := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	stage(func() { p.tail(runCtx, lines) })
	stage(func() { p.parse(lines, events) })
	stage(func() { p.batch(events, pings) })
	var upErr error
	done := make(chan struct{})
	go func() {
		upErr = p.upload(runCtx, pings)
		cancel()
		drainAll(pings) // let the other stages finish if upload stopped early
		close(done)
	}()
	wg.Wait()
	<-done
	return upErr
}

func (p *Pipeline) tail(ctx context.Context, lines chan<- string) {
	defer close(lines)
	p.setState(StateWatching, "")
	_ = p.o.Source.Run(ctx, func(line string) {
		select {
		case lines <- line:
		case <-ctx.Done():
		}
	})
}

func (p *Pipeline) parse(lines <-chan string, events chan<- parser.Event) {
	defer close(events)
	for line := range lines {
		p.Stats.Lines.Add(1)
		ev, err := parser.ParseLine(line, nil)
		if err != nil {
			continue
		}
		events <- ev
	}
}

func (p *Pipeline) batch(events <-chan parser.Event, out chan<- domain.Ping) {
	defer close(out)
	b := NewBatcher(p.o.Batch)
	tk := time.NewTicker(p.o.Interval)
	defer tk.Stop()
	flush := func() {
		pings, err := b.Flush(p.o.Clock.Now())
		if err != nil {
			p.activity("Could not prepare an upload.")
		}
		for _, ping := range pings {
			p.Stats.Contacts.Store(int64(b.ContactsSent()))
			out <- ping
		}
	}
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				flush()
				return
			}
			b.Apply(ev)
		case <-tk.C:
			flush()
		}
	}
}

// upload queues every ping, then sends the queue oldest first. It returns ErrKeyRejected on 401/403.
func (p *Pipeline) upload(ctx context.Context, pings <-chan domain.Ping) error {
	backoff, retryAt := p.o.MinBackoff, time.Time{}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		select {
		case ping, ok := <-pings:
			if !ok {
				return p.finalFlush()
			}
			p.enqueue(ping)
		case <-timer.C:
		}
		if time.Now().Before(retryAt) { // real time: backoff is about the network, not log time
			continue
		}
		wait, err := p.drain(ctx)
		if err != nil {
			return err
		}
		if wait {
			retryAt = time.Now().Add(backoff)
			timer.Reset(backoff)
			backoff = min(backoff*2, maxBackoff)
		} else {
			backoff, retryAt = p.o.MinBackoff, time.Time{}
		}
	}
}

func (p *Pipeline) enqueue(ping domain.Ping) {
	if err := domain.ValidatePing(ping, p.o.Clock.Now()); err != nil {
		p.activity("A batch was not valid and was skipped.") // a bug indicator; no content
		return
	}
	data, err := json.Marshal(ping)
	if err != nil || p.o.Queue.Push(data) != nil {
		p.activity("Could not store a batch for upload.")
		return
	}
	p.Stats.Dropped.Store(int64(p.o.Queue.Dropped()))
}

// drain sends queued pings oldest first. wait is true when a retryable failure means "try again later".
func (p *Pipeline) drain(ctx context.Context) (wait bool, err error) {
	for {
		data, perr := p.o.Queue.Peek()
		if perr != nil {
			return false, nil
		}
		var ping domain.Ping
		if json.Unmarshal(data, &ping) != nil {
			p.o.Queue.Pop()
			continue
		}
		_, serr := p.o.Sender.PostPing(ctx, ping)
		switch {
		case serr == nil:
			p.sent()
		case ctx.Err() != nil:
			return false, nil
		case apiclient.IsAuth(serr):
			p.setState(StateError, "API key rejected")
			return false, ErrKeyRejected
		case apiclient.IsRetryable(serr):
			p.setState(StateError, "Can't reach the server. Will keep trying.")
			return true, nil
		default:
			p.o.Queue.Pop()
			p.activity("The server refused a batch (" + errCode(serr) + ").")
		}
	}
}

func (p *Pipeline) sent() {
	p.o.Queue.Pop()
	p.Stats.PingsSent.Add(1)
	p.Stats.LastSuccess.Store(p.o.Clock.Now().UnixMilli())
	p.setState(StateUploading, "")
}

func errCode(err error) string {
	var ae *apiclient.APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return "error"
}

// finalFlush is the last upload attempt after Stop, bounded by finalFlushTimeout.
func (p *Pipeline) finalFlush() error {
	ctx, cancel := context.WithTimeout(context.Background(), finalFlushTimeout)
	defer cancel()
	_, err := p.drain(ctx)
	return err
}

var _ Source = (*tailer.Follower)(nil)

// drainAll discards what is left on ch until it is closed.
func drainAll(ch <-chan domain.Ping) {
	for range ch {
		_ = 0
	}
}
