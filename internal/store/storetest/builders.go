package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// env is what every rule receives: a fresh store, its clock and a context.
type env struct {
	t     *testing.T
	s     store.Store
	clock *FakeClock
	ctx   context.Context
	seq   int
	fresh func() store.Store // a second, empty store sharing the clock
}

// ms converts a time to the epoch milliseconds the wire format uses.
func ms(t time.Time) int64 { return t.UnixMilli() }

// ago is epoch milliseconds d before the clock's now.
func (e *env) ago(d time.Duration) int64 { return ms(e.clock.Now().Add(-d)) }

type pingOpt func(*domain.Ping)

// aPing builds a valid-looking ping with a fresh batch id.
func (e *env) aPing(opts ...pingOpt) domain.Ping {
	e.seq++
	p := domain.Ping{
		BatchID: fmt.Sprintf("batch-%04d", e.seq), Source: "suite",
		Ruleset: domain.RulesetPvP, SentAt: ms(e.clock.Now()),
	}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func withRuleset(r domain.Ruleset) pingOpt { return func(p *domain.Ping) { p.Ruleset = r } }
func withContacts(c ...domain.Contact) pingOpt {
	return func(p *domain.Ping) { p.Contacts = append(p.Contacts, c...) }
}
func withCharts(c ...domain.Chart) pingOpt {
	return func(p *domain.Ping) { p.Charts = append(p.Charts, c...) }
}
func withKills(k ...domain.Kill) pingOpt {
	return func(p *domain.Ping) { p.Kills = append(p.Kills, k...) }
}

// aContact builds a hostile contact whose lastSeen is given.
func aContact(guid, name string, lastSeen int64, fixes ...domain.Fix) domain.Contact {
	return domain.Contact{GUID: guid, Name: name, Raw: name + "-Realm", Reaction: domain.ReactionHostile, LastSeen: lastSeen, Fixes: fixes}
}

// aFix builds an exact fix.
func aFix(t int64, mapID int, x, y float64) domain.Fix {
	return domain.Fix{T: t, MapID: mapID, X: x, Y: y, Kind: domain.FixExact}
}

func aKill(t int64, killer, victim string) domain.Kill {
	return domain.Kill{T: t, Killer: killer, Victim: victim, MapID: 1411}
}

func aChart(mapID int, zone string, x1, y1 float64) domain.Chart {
	return domain.Chart{MapID: mapID, Zone: zone, X0: 0, Y0: 0, X1: x1, Y1: y1}
}

// guid makes a well-formed player GUID from a small number.
func guid(n int) string { return fmt.Sprintf("Player-1329-%08X", n) }

// apply stores a ping at the clock's now and fails the test on error.
func (e *env) apply(p domain.Ping) store.ApplyResult {
	e.t.Helper()
	r, err := e.s.ApplyPing(e.ctx, p, e.clock.Now())
	if err != nil {
		e.t.Fatalf("ApplyPing(%s): %v", p.BatchID, err)
	}
	return r
}

func (e *env) version(rs domain.Ruleset) int64 {
	e.t.Helper()
	v, err := e.s.Version(e.ctx, rs)
	if err != nil {
		e.t.Fatalf("Version(%s): %v", rs, err)
	}
	return v
}

func (e *env) sweep(rs domain.Ruleset) []domain.SweepContact {
	e.t.Helper()
	got, err := e.s.Sweep(e.ctx, store.SweepQuery{Ruleset: rs, Now: e.clock.Now()})
	if err != nil {
		e.t.Fatalf("Sweep: %v", err)
	}
	return got
}

func (e *env) trail(g string, f store.TrailFilter) store.Trail {
	e.t.Helper()
	got, err := e.s.Trail(e.ctx, domain.RulesetPvP, g, f)
	if err != nil {
		e.t.Fatalf("Trail(%s): %v", g, err)
	}
	return got
}

func (e *env) search(rs domain.Ruleset, q string) []store.PlayerSummary {
	e.t.Helper()
	got, err := e.s.SearchPlayers(e.ctx, rs, q, store.MaxPlayerRows)
	if err != nil {
		e.t.Fatalf("SearchPlayers(%q): %v", q, err)
	}
	return got
}

func (e *env) bounty(rs domain.Ruleset) []domain.KillTotal {
	e.t.Helper()
	got, err := e.s.Bounty(e.ctx, rs, store.MaxBountyRows)
	if err != nil {
		e.t.Fatalf("Bounty: %v", err)
	}
	return got
}

// guids lists the GUIDs of a slice of sweep contacts in order.
func sweepGUIDs(cs []domain.SweepContact) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.GUID
	}
	return out
}
