package pipeline

import (
	"fmt"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/parser"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// countRand yields a never-repeating byte stream, so generated batch ids differ.
type countRand struct{ n uint64 }

func (r *countRand) Read(p []byte) (int, error) {
	for i := range p {
		r.n++
		p[i] = byte(r.n * 2654435761 >> 7)
	}
	return len(p), nil
}

func zeroRand() *countRand { return &countRand{} }

func mapEv() parser.Event {
	return parser.Event{Type: "MAP_CHANGE", Map: &parser.MapBounds{ID: 1426, Name: "Dun Morogh", X0: -6000, X1: -4000, Y0: -1000, Y1: 1000}}
}

func playerEv(i int) parser.Event {
	g := fmt.Sprintf("Player-1-%08X", i)
	return parser.Event{Time: t0, Type: "SPELL_CAST_SUCCESS",
		Src:  parser.Unit{GUID: g, Name: fmt.Sprintf("N%d-Realm-PvP-US", i), Flags: 0x548},
		Info: &parser.Info{GUID: g, X: -5000, Y: 0, MapID: 1426, HasPos: true}}
}

func newBatcher(rules string) *Batcher {
	return NewBatcher(BatcherOptions{Source: "test", Ruleset: rules, MaxContacts: 200, Rand: zeroRand()})
}

func TestBatcher_EmptyFlushSendsNothing(t *testing.T) {
	b := newBatcher("auto")
	pings, err := b.Flush(t0)
	if err != nil || len(pings) != 0 {
		t.Errorf("pings=%d err=%v, want none", len(pings), err)
	}
}

func TestBatcher_SplitsAtMaxContactsAndPingsValidate(t *testing.T) {
	b := newBatcher("auto")
	b.Apply(mapEv())
	for i := 0; i < 450; i++ {
		b.Apply(playerEv(i))
	}
	pings, err := b.Flush(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 3 || len(pings[0].Contacts) != 200 || len(pings[2].Contacts) != 50 {
		t.Fatalf("got %d pings", len(pings))
	}
	seen := map[string]bool{}
	for _, p := range pings {
		if err := domain.ValidatePing(p, t0); err != nil {
			t.Fatalf("invalid ping: %v", err)
		}
		if seen[p.BatchID] {
			t.Errorf("batchId %s reused", p.BatchID)
		}
		seen[p.BatchID] = true
	}
}

func TestBatcher_BatchIDsAreUnique(t *testing.T) {
	b := NewBatcher(BatcherOptions{Source: "t", Ruleset: "pvp", MaxContacts: 1, Rand: zeroRand()})
	b.Apply(mapEv())
	b.Apply(playerEv(1))
	b.Apply(playerEv(2))
	pings, _ := b.Flush(t0)
	if len(pings) != 2 || pings[0].BatchID == pings[1].BatchID || !domain.IsUUIDv4(pings[0].BatchID) {
		t.Errorf("ids %q %q", pings[0].BatchID, pings[1].BatchID)
	}
}

func TestBatcher_DeterministicOrder(t *testing.T) {
	b := newBatcher("pvp")
	b.Apply(mapEv())
	for _, i := range []int{5, 1, 9, 3} {
		b.Apply(playerEv(i))
	}
	pings, _ := b.Flush(t0)
	for i := 1; i < len(pings[0].Contacts); i++ {
		if pings[0].Contacts[i-1].GUID >= pings[0].Contacts[i].GUID {
			t.Fatal("contacts not sorted by GUID")
		}
	}
}

func TestBatcher_Ruleset(t *testing.T) {
	tests := []struct {
		name, cfg string
		want      domain.Ruleset
	}{
		{"configured wins", "rp", domain.RulesetRP},
		{"detected from realm token", "auto", domain.RulesetPvP},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBatcher(tc.cfg)
			b.Apply(mapEv())
			b.Apply(playerEv(1))
			pings, _ := b.Flush(t0)
			if pings[0].Ruleset != tc.want {
				t.Errorf("ruleset = %s, want %s", pings[0].Ruleset, tc.want)
			}
		})
	}
}

func TestBatcher_AutoDetectsRPPvP(t *testing.T) {
	b := newBatcher("auto")
	b.Apply(mapEv())
	e := playerEv(1)
	e.Src.Name = "Nia-Realm-RP-PvP-US"
	b.Apply(e)
	pings, _ := b.Flush(t0)
	if pings[0].Ruleset != domain.RulesetRPPvP {
		t.Errorf("ruleset = %s", pings[0].Ruleset)
	}
}

func TestBatcher_KillsRideAlongWithCharts(t *testing.T) {
	b := newBatcher("pvp")
	b.Apply(mapEv())
	a, v := playerEv(1), playerEv(2)
	b.Apply(a)
	b.Apply(v)
	b.Apply(parser.Event{Time: t0, Type: "SPELL_DAMAGE", IsDamage: true, Src: a.Src, Dst: v.Src, Info: v.Info})
	b.Apply(parser.Event{Time: t0.Add(time.Second), Type: "UNIT_DIED", Dst: v.Src})
	pings, _ := b.Flush(t0)
	if len(pings[0].Kills) != 1 || len(pings[0].Charts) != 1 {
		t.Fatalf("kills=%d charts=%d", len(pings[0].Kills), len(pings[0].Charts))
	}
	if err := domain.ValidatePing(pings[0], t0); err != nil {
		t.Fatal(err)
	}
}
