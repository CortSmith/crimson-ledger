package pipeline

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/clock"
)

// Lines are from a real-format log; the clock is set to their instant so pings validate.
const (
	mapLine = `11/10/2026 15:10:53.403-6  MAP_CHANGE,1426,"Dun Morogh",-3877.083252,-7160.416504,1802.083252,-3122.916504`
	hitLine = `11/10/2026 15:11:47.075-6  SWING_DAMAGE,Player-1-0000AAAA,"Alpha-Realm-PvP-US",0x548,0x80000000,Creature-0-6835-0-11-1397-0000F38640,"Boar",0xa28,0x80000000,Player-1-0000AAAA,0000000000000000,56,147,90,0,171,0,0,0,3,35,105,0,-5370.61,-224.12,1426,1.8470,12,6,11,-1,1,0,0,0,nil,1,nil`
)

var logTime = time.Date(2026, 11, 10, 21, 11, 47, 0, time.UTC)

type fakeSource struct{ lines []string }

func (f fakeSource) Run(ctx context.Context, handle func(string)) error {
	for _, l := range f.lines {
		handle(l)
	}
	<-ctx.Done()
	return ctx.Err()
}

type fakeSender struct {
	mu      sync.Mutex
	results []error // consumed one per call; nil entries and an exhausted list mean success
	got     []domain.Ping
}

func (f *fakeSender) PostPing(_ context.Context, p domain.Ping) (apiclient.PingResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, p)
	if len(f.results) == 0 {
		return apiclient.PingResult{Accepted: true}, nil
	}
	err := f.results[0]
	f.results = f.results[1:]
	return apiclient.PingResult{Accepted: err == nil}, err
}

func (f *fakeSender) calls() []domain.Ping {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Ping(nil), f.got...)
}

type recorder struct {
	mu     sync.Mutex
	states []State
	acts   []string
}

func (r *recorder) state(s State, _ string) {
	r.mu.Lock()
	r.states = append(r.states, s)
	r.mu.Unlock()
}
func (r *recorder) act(m string) { r.mu.Lock(); r.acts = append(r.acts, m); r.mu.Unlock() }
func (r *recorder) last() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.states) == 0 {
		return ""
	}
	return r.states[len(r.states)-1]
}

func apiErr(status int) error {
	return &apiclient.APIError{Status: status, Code: http.StatusText(status), Message: "x"}
}

func newPipeline(t *testing.T, send Sender, rec *recorder) *Pipeline {
	t.Helper()
	q, err := OpenQueue(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Source: fakeSource{lines: []string{mapLine, hitLine}}, Sender: send, Queue: q,
		Batch: BatcherOptions{Source: "test", Ruleset: "auto", MaxContacts: 200}, Interval: 10 * time.Millisecond,
		Clock: clock.NewFake(logTime), Rand: zeroRand(), MinBackoff: time.Millisecond,
		OnState: rec.state, OnActivity: rec.act})
}

func runUntil(t *testing.T, p *Pipeline, cond func() bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- p.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case err := <-errc:
			return err
		case <-deadline:
			cancel()
			t.Fatal("condition not reached in time")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	return <-errc
}

func TestPipeline_EndToEnd_LinesBecomeOnePing(t *testing.T) {
	send, rec := &fakeSender{}, &recorder{}
	p := newPipeline(t, send, rec)
	if err := runUntil(t, p, func() bool { return len(send.calls()) >= 1 }); err != nil {
		t.Fatal(err)
	}
	got := send.calls()[0]
	if len(got.Contacts) != 1 || got.Contacts[0].GUID != "Player-1-0000AAAA" || !strings.HasPrefix(got.Contacts[0].Name, "Alpha") {
		t.Errorf("ping = %+v", got)
	}
	if p.Stats.Lines.Load() != 2 || p.Stats.PingsSent.Load() < 1 || rec.last() != StateUploading {
		t.Errorf("lines=%d sent=%d state=%s", p.Stats.Lines.Load(), p.Stats.PingsSent.Load(), rec.last())
	}
}

func TestPipeline_RetriesWithSameBatchID(t *testing.T) {
	send := &fakeSender{results: []error{apiErr(503), apiErr(503), apiErr(503)}}
	rec := &recorder{}
	p := newPipeline(t, send, rec)
	if err := runUntil(t, p, func() bool { return p.Stats.PingsSent.Load() >= 1 }); err != nil {
		t.Fatal(err)
	}
	calls := send.calls()
	if len(calls) < 4 {
		t.Fatalf("calls = %d, want 3 failures + 1 success", len(calls))
	}
	for _, c := range calls[:4] {
		if c.BatchID != calls[0].BatchID {
			t.Fatalf("batchId changed on retry: %s vs %s", c.BatchID, calls[0].BatchID)
		}
	}
}

func TestPipeline_KeyRejectedStopsAndReports(t *testing.T) {
	send := &fakeSender{results: []error{apiErr(401)}}
	rec := &recorder{}
	p := newPipeline(t, send, rec)
	err := runUntil(t, p, func() bool { return false })
	if !errors.Is(err, ErrKeyRejected) || rec.last() != StateError {
		t.Fatalf("err=%v state=%s", err, rec.last())
	}
	if n := len(send.calls()); n != 1 {
		t.Errorf("sent %d times after a rejection, want 1", n)
	}
}

func TestPipeline_RefusedPingIsDroppedWithoutDetails(t *testing.T) {
	send := &fakeSender{results: []error{apiErr(400)}}
	rec := &recorder{}
	p := newPipeline(t, send, rec)
	if err := runUntil(t, p, func() bool { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.acts) > 0 }); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.Contains(rec.acts[0], "refused") || p.o.Queue.Len() != 0 {
		t.Errorf("acts=%v queued=%d", rec.acts, p.o.Queue.Len())
	}
}

func TestPipeline_StopFlushesPending(t *testing.T) {
	send, rec := &fakeSender{}, &recorder{}
	p := newPipeline(t, send, rec)
	p.o.Interval = time.Hour // only the stop-time flush can send
	err := runUntil(t, p, func() bool { return p.Stats.Lines.Load() >= 2 })
	if err != nil || len(send.calls()) != 1 {
		t.Fatalf("err=%v sent=%d, want the pending contact flushed on stop", err, len(send.calls()))
	}
}

func TestPipeline_NeverPutsKeyOrRawLinesInActivity(t *testing.T) {
	send := &fakeSender{results: []error{apiErr(400)}}
	rec := &recorder{}
	p := newPipeline(t, send, rec)
	_ = runUntil(t, p, func() bool { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.acts) > 0 })
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, a := range rec.acts {
		if strings.Contains(a, "Alpha") || strings.Contains(a, "Player-") {
			t.Errorf("activity leaks log content: %q", a)
		}
	}
}
